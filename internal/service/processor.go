package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/repository"
	"github.com/nkowanitemwani/amp-digital-library-backend/storage"
)

// =============================================================
// PROCESSOR
// Manages a pool of worker goroutines that process uploaded books.
// For each book, three pipelines run in parallel:
//   1. Standard audio     — full text read aloud by one voice
//   2. Teaching dialogue  — two-voice Groq-generated conversation
//   3. Quiz questions     — AI-generated MCQs with individual audio
//
// Standard audio failure is fatal — the book cannot be used without it.
// Dialogue and question failures are non-fatal — the book is still
// marked ready for standard listening if audio succeeds.
// =============================================================

// DialogueLine is one spoken turn in the teaching dialogue.
type DialogueLine struct {
	Speaker string `json:"speaker"` // "A" = teacher, "B" = curious student voice
	Text    string `json:"text"`
}

// QuizQuestion is one AI-generated multiple choice question from Groq.
type QuizQuestion struct {
	Question     string   `json:"question"`
	Options      []string `json:"options"`       // exactly 4 options
	CorrectIndex int      `json:"correct_index"` // zero-based
}

// Processor holds the dependencies shared across all workers.
type Processor struct {
	bookRepo     *repository.BookRepository
	questionRepo *repository.QuestionRepository
	auditRepo    *repository.AuditRepository
	store        storage.Storage

	// Two ElevenLabs voices for the teaching dialogue.
	// VoiceA is also used for standard audio and question audio.
	elevenLabsKey    string
	elevenLabsVoiceA string // Teacher A — explains concepts
	elevenLabsVoiceB string // Teacher B — asks questions like a student

	// Groq API key for dialogue and question generation.
	groqKey string

	workerCount  int
	pollInterval time.Duration
}

// NewProcessor creates a Processor with all dependencies injected.
// No AWS setup here — credentials come from config in main.go.
func NewProcessor(
	bookRepo *repository.BookRepository,
	questionRepo *repository.QuestionRepository,
	auditRepo *repository.AuditRepository,
	store storage.Storage,
	elevenLabsKey, elevenLabsVoiceA, elevenLabsVoiceB string,
	groqKey string,
	workerCount, pollSecs int,
) (*Processor, error) {
	return &Processor{
		bookRepo:         bookRepo,
		questionRepo:     questionRepo,
		auditRepo:        auditRepo,
		store:            store,
		elevenLabsKey:    elevenLabsKey,
		elevenLabsVoiceA: elevenLabsVoiceA,
		elevenLabsVoiceB: elevenLabsVoiceB,
		groqKey:          groqKey,
		workerCount:      workerCount,
		pollInterval:     time.Duration(pollSecs) * time.Second,
	}, nil
}

// Start launches workerCount goroutines and returns immediately.
// ctx controls the lifetime of all workers — cancel it on shutdown.
func (p *Processor) Start(ctx context.Context) {
	for i := 1; i <= p.workerCount; i++ {
		go p.runWorker(ctx, i)
	}
	log.Printf("processor: started %d workers (poll interval: %s)", p.workerCount, p.pollInterval)
}

// =============================================================
// WORKER LOOP
// =============================================================

func (p *Processor) runWorker(ctx context.Context, workerID int) {
	log.Printf("processor: worker %d started", workerID)
	for {
		select {
		case <-ctx.Done():
			log.Printf("processor: worker %d shutting down", workerID)
			return
		default:
		}

		processed, err := p.claimAndProcess(ctx, workerID)
		if err != nil {
			log.Printf("processor: worker %d error: %v", workerID, err)
		}
		if !processed {
			select {
			case <-ctx.Done():
				return
			case <-time.After(p.pollInterval):
			}
		}
	}
}

// =============================================================
// CLAIM AND PROCESS
// =============================================================

func (p *Processor) claimAndProcess(ctx context.Context, workerID int) (bool, error) {
	book, tx, err := p.bookRepo.ClaimNextPending(ctx)
	if err != nil {
		return false, fmt.Errorf("claim pending book: %w", err)
	}
	if book == nil {
		return false, nil
	}

	log.Printf("processor: worker %d claimed book %s (%s)", workerID, book.ID, book.Title)

	// Fetch and extract text — shared input for all three pipelines.
	pdfData, err := p.fetchPDF(ctx, book)
	if err != nil {
		tx.Rollback()
		p.markFailed(ctx, book, fmt.Sprintf("fetch pdf: %v", err))
		return true, nil
	}

	text, err := extractText(pdfData)
	if err != nil {
		tx.Rollback()
		p.markFailed(ctx, book, fmt.Sprintf("extract text: %v", err))
		return true, nil
	}

	// Run standard audio and dialogue in parallel.
	// Questions run in a separate goroutine independently.
	type audioResult struct {
		path string
		err  error
	}
	type dialogResult struct {
		path string
		err  error
	}

	audioCh := make(chan audioResult, 1)
	dialogCh := make(chan dialogResult, 1)

	go func() {
		audio, err := p.synthesise(ctx, text)
		if err != nil {
			audioCh <- audioResult{err: err}
			return
		}
		key := storage.AudioKey(book.ID)
		if err := p.store.Save(ctx, key, audio, "audio/mpeg"); err != nil {
			audioCh <- audioResult{err: fmt.Errorf("save audio: %w", err)}
			return
		}
		audioCh <- audioResult{path: key}
	}()

	go func() {
		lines, err := p.generateDialogue(ctx, text)
		if err != nil {
			dialogCh <- dialogResult{err: fmt.Errorf("generate dialogue: %w", err)}
			return
		}
		audio, err := p.synthesiseDialogue(ctx, lines)
		if err != nil {
			dialogCh <- dialogResult{err: fmt.Errorf("synthesise dialogue: %w", err)}
			return
		}
		key := storage.DialogueKey(book.ID)
		if err := p.store.Save(ctx, key, audio, "audio/mpeg"); err != nil {
			dialogCh <- dialogResult{err: fmt.Errorf("save dialogue: %w", err)}
			return
		}
		dialogCh <- dialogResult{path: key}
	}()

	// Questions run fully independently — success/failure does not affect audio.
	go p.generateAndSaveQuestions(ctx, book, text)

	ar := <-audioCh
	dr := <-dialogCh

	// Standard audio failure → fail the whole book.
	if ar.err != nil {
		tx.Rollback()
		p.markFailed(ctx, book, ar.err.Error())
		return true, nil
	}

	// Dialogue failure is non-fatal — log and continue.
	dialoguePath := ""
	if dr.err != nil {
		log.Printf("processor: worker %d dialogue failed for %s (non-fatal): %v", workerID, book.ID, dr.err)
	} else {
		dialoguePath = dr.path
	}

	// Commit standard audio to the book row inside the claim transaction.
	if err := p.bookRepo.UpdateAudioReady(ctx, tx, book.ID, ar.path, book.Version); err != nil {
		tx.Rollback()
		p.markFailed(ctx, book, fmt.Sprintf("update audio ready: %v", err))
		return true, nil
	}

	if err := tx.Commit(); err != nil {
		p.markFailed(ctx, book, fmt.Sprintf("commit: %v", err))
		return true, nil
	}

	// Update dialogue path outside the transaction — it does not affect
	// the book's primary ready status.
	if dialoguePath != "" {
		if err := p.bookRepo.UpdateDialogueReady(ctx, book.ID, dialoguePath); err != nil {
			log.Printf("processor: could not save dialogue path for %s: %v", book.ID, err)
		}
	}

	log.Printf("processor: worker %d completed book %s", workerID, book.ID)

	p.auditRepo.Log(ctx, &models.AuditEntry{
		SchoolID: &book.SchoolID,
		Action:   "book.processed",
		Entity:   "book",
		EntityID: &book.ID,
	})

	return true, nil
}

// =============================================================
// PDF FETCH + TEXT EXTRACTION
// =============================================================

func (p *Processor) fetchPDF(ctx context.Context, book *models.Book) ([]byte, error) {
	if book.PDFPath == nil || *book.PDFPath == "" {
		return nil, fmt.Errorf("book has no pdf_path")
	}
	data, err := readFromStore(ctx, p.store, *book.PDFPath)
	if err != nil {
		return nil, fmt.Errorf("read pdf from storage: %w", err)
	}
	return data, nil
}

func extractText(pdfData []byte) (string, error) {
	reader := bytes.NewReader(pdfData)
	pdfReader, err := pdf.NewReader(reader, int64(len(pdfData)))
	if err != nil {
		return "", fmt.Errorf("open pdf reader: %w", err)
	}

	var sb strings.Builder
	for pageNum := 1; pageNum <= pdfReader.NumPage(); pageNum++ {
		page := pdfReader.Page(pageNum)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			log.Printf("processor: warning — could not extract page %d: %v", pageNum, err)
			continue
		}
		sb.WriteString(text)
		sb.WriteString("\n")
	}

	extracted := sb.String()
	if strings.TrimSpace(extracted) == "" {
		return "", fmt.Errorf("no text could be extracted from pdf")
	}
	return extracted, nil
}

// =============================================================
// STANDARD AUDIO — single ElevenLabs voice
// =============================================================

func (p *Processor) synthesise(ctx context.Context, text string) ([]byte, error) {
	const maxChunkSize = 2500
	chunks := splitIntoChunks(text, maxChunkSize)
	log.Printf("processor: synthesising %d standard audio chunk(s)", len(chunks))

	var fullAudio []byte
	for i, chunk := range chunks {
		audio, err := p.elevenLabsTTS(ctx, chunk, p.elevenLabsVoiceA)
		if err != nil {
			return nil, fmt.Errorf("standard audio chunk %d of %d: %w", i+1, len(chunks), err)
		}
		fullAudio = append(fullAudio, audio...)
	}
	return fullAudio, nil
}

// =============================================================
// GROQ — shared API call helper
// =============================================================

// callGroq sends a system + user prompt to Groq and returns the
// raw content string from the first choice. Both dialogue generation
// and question generation use this helper.
func (p *Processor) callGroq(ctx context.Context, systemPrompt, userContent string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": "llama-3.1-8b-instant",
		"messages": []map[string]any{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userContent},
		},
		"temperature":     0.7,
		"response_format": map[string]string{"type": "json_object"},
	})

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.groq.com/openai/v1/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("create groq request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.groqKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("groq http: %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode groq response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("groq returned no choices")
	}
	return out.Choices[0].Message.Content, nil
}

// =============================================================
// TEACHING DIALOGUE — Groq generation + two-voice ElevenLabs
// =============================================================

// generateDialogue converts extracted text into a two-voice educational
// conversation using Groq. The conversation is designed for visually
// impaired primary school students — simple language, southern African
// context, key points recapped at the end.
func (p *Processor) generateDialogue(ctx context.Context, text string) ([]DialogueLine, error) {
	system := `You create educational audio lessons for visually impaired primary school 
students aged 6 to 13 in Zambia and southern Africa.

Convert the provided textbook content into a natural engaging conversation between:
- Teacher A: explains concepts clearly using simple language and real-world examples 
  from southern Africa such as local animals, food, and places students would recognise.
- Teacher B: asks the curious questions a student would ask, shows surprise or delight
  at interesting facts, and helps summarise key points at the end.

Rules:
- Use very simple vocabulary appropriate for primary school level.
- Keep each line short — no more than 2 sentences per turn.
- Cover all the key concepts from the content naturally through conversation.
- End with Teacher A clearly recapping the 2 or 3 most important things to remember.
- Aim for 20 exchanges total.

Return ONLY valid JSON in this exact format with no extra text:
{"dialogue": [{"speaker": "A", "text": "..."}, {"speaker": "B", "text": "..."}, ...]}`

	raw, err := p.callGroq(ctx, system, text)
	if err != nil {
		return nil, err
	}

	var out struct {
		Dialogue []DialogueLine `json:"dialogue"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse dialogue JSON: %w", err)
	}
	if len(out.Dialogue) == 0 {
		return nil, fmt.Errorf("groq returned empty dialogue")
	}
	return out.Dialogue, nil
}

// synthesiseDialogue converts each dialogue line to audio using the
// correct voice (A or B) and concatenates the MP3 chunks in order.
func (p *Processor) synthesiseDialogue(ctx context.Context, lines []DialogueLine) ([]byte, error) {
	log.Printf("processor: synthesising dialogue (%d lines)", len(lines))

	var fullAudio []byte
	for i, line := range lines {
		voice := p.elevenLabsVoiceA
		if line.Speaker == "B" {
			voice = p.elevenLabsVoiceB
		}
		audio, err := p.elevenLabsTTS(ctx, line.Text, voice)
		if err != nil {
			return nil, fmt.Errorf("dialogue line %d (speaker %s): %w", i+1, line.Speaker, err)
		}
		fullAudio = append(fullAudio, audio...)
	}
	return fullAudio, nil
}

// =============================================================
// QUIZ QUESTIONS — Groq generation + per-question audio
// =============================================================

// generateAndSaveQuestions generates 4 multiple choice questions via Groq,
// synthesises audio for each, and saves everything to the DB and S3.
// Runs in its own goroutine and is fully non-fatal — failures are logged
// but never propagate to the book's primary processing status.
func (p *Processor) generateAndSaveQuestions(ctx context.Context, book *models.Book, text string) {
	system := `You generate multiple choice quiz questions for primary school students
aged 6 to 13 in Zambia. Questions must test understanding of the key concepts.

Requirements:
- Generate exactly 4 questions.
- Each question has exactly 4 answer options.
- Use simple clear language appropriate for primary school.
- correct_index is zero-based meaning 0 1 2 or 3.
- Make sure only one option is clearly correct.
- Questions should cover different parts of the content.

Return ONLY valid JSON in this exact format with no extra text:
{"questions": [{"question": "...", "options": ["...", "...", "...", "..."], "correct_index": 0}]}`

	raw, err := p.callGroq(ctx, system, text)
	if err != nil {
		log.Printf("processor: question generation failed for book %s: %v", book.ID, err)
		return
	}

	var out struct {
		Questions []QuizQuestion `json:"questions"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		log.Printf("processor: question parse failed for book %s: %v", book.ID, err)
		return
	}
	if len(out.Questions) == 0 {
		log.Printf("processor: groq returned no questions for book %s", book.ID)
		return
	}

	// Build Question models.
	questions := make([]*models.Question, len(out.Questions))
	for i, q := range out.Questions {
		questions[i] = &models.Question{
			BookID:       book.ID,
			GradeID:      book.GradeID,
			QuestionText: q.Question,
			Options:      q.Options,
			CorrectIndex: q.CorrectIndex,
			OrderIndex:   i,
		}
	}

	// Persist all questions atomically before synthesising audio.
	// This gives each question a UUID we need for the audio S3 key.
	if err := p.questionRepo.BulkCreate(ctx, questions); err != nil {
		log.Printf("processor: bulk create questions failed for book %s: %v", book.ID, err)
		return
	}

	// Synthesise audio for each question.
	// The student hears: "Question 1. [question]. Press 1 for [option 1].
	// Press 2 for [option 2]. Press 3 for [option 3]. Press 4 for [option 4]."
	// This is the complete accessible interface — no screen reading required.
	for _, q := range questions {
		if len(q.Options) < 4 {
			continue
		}

		spoken := fmt.Sprintf(
			"Question %d. %s. Press 1 for %s. Press 2 for %s. Press 3 for %s. Press 4 for %s.",
			q.OrderIndex+1,
			q.QuestionText,
			q.Options[0], q.Options[1], q.Options[2], q.Options[3],
		)

		audio, err := p.elevenLabsTTS(ctx, spoken, p.elevenLabsVoiceA)
		if err != nil {
			log.Printf("processor: question audio failed for %s: %v", q.ID, err)
			continue
		}

		key := storage.QuestionAudioKey(q.ID)
		if err := p.store.Save(ctx, key, audio, "audio/mpeg"); err != nil {
			log.Printf("processor: save question audio failed for %s: %v", q.ID, err)
			continue
		}

		if err := p.questionRepo.UpdateAudioPath(ctx, q.ID, key); err != nil {
			log.Printf("processor: update question audio path failed for %s: %v", q.ID, err)
		}
	}

	log.Printf("processor: generated and saved %d questions for book %s", len(questions), book.ID)
}

// =============================================================
// ELEVENLABS TTS — used by all three audio pipelines
// =============================================================

// elevenLabsTTS converts a text string to MP3 using ElevenLabs.
// voiceID is passed explicitly so standard audio, dialogue A, dialogue B,
// and question audio can each use the correct voice.
func (p *Processor) elevenLabsTTS(ctx context.Context, text, voiceID string) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"text":     text,
		"model_id": "eleven_turbo_v2_5",
		"voice_settings": map[string]any{
			"stability":         0.5,
			"similarity_boost":  0.75,
			"style":             0.0,
			"use_speaker_boost": true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal tts request: %w", err)
	}

	url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s", voiceID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create tts request: %w", err)
	}
	req.Header.Set("xi-api-key", p.elevenLabsKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("elevenlabs %d: %s", resp.StatusCode, string(errBody))
	}

	return io.ReadAll(resp.Body)
}

// =============================================================
// SHARED HELPERS
// =============================================================

func splitIntoChunks(text string, maxSize int) []string {
	if len(text) <= maxSize {
		return []string{text}
	}
	var chunks []string
	words := strings.Fields(text)
	var current strings.Builder
	for _, word := range words {
		if current.Len()+len(word)+1 > maxSize && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString(" ")
		}
		current.WriteString(word)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func (p *Processor) markFailed(ctx context.Context, book *models.Book, reason string) {
	failCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := p.bookRepo.UpdateStatusFailed(failCtx, book.ID); err != nil {
		log.Printf("processor: could not mark book %s as failed: %v", book.ID, err)
	}

	p.auditRepo.Log(failCtx, &models.AuditEntry{
		SchoolID: &book.SchoolID,
		Action:   "book.processing_failed",
		Entity:   "book",
		EntityID: &book.ID,
		Metadata: map[string]string{"reason": reason},
	})
}

func readFromStore(ctx context.Context, store storage.Storage, key string) ([]byte, error) {
	switch s := store.(type) {
	case *storage.LocalStorage:
		return s.ReadFile(key)
	case *storage.S3Storage:
		return s.GetObject(ctx, key)
	default:
		return nil, fmt.Errorf("readFromStore: unsupported storage type %T", store)
	}
}
