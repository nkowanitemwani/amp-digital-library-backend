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
// Each worker independently polls the database for the next pending
// book, processes it (extract text → synthesise audio → upload),
// then updates the book status.
//
// Workers coordinate safely using SELECT FOR UPDATE SKIP LOCKED in
// the repository — no worker-level locking or channels are needed.
// =============================================================

// Processor holds the dependencies shared across all workers.
// Created once in main.go and started with Start().
type Processor struct {
	bookRepo    *repository.BookRepository
	auditRepo   *repository.AuditRepository
	store       storage.Storage
	elevenLabsKey     string
    elevenLabsVoiceID string

	// workerCount controls how many goroutines run concurrently.
	// Each worker holds one DB connection for the duration of a claim,
	// so this should not exceed the pool's MaxOpenConns.
	workerCount int

	// pollInterval is how long a worker sleeps when the queue is empty.
	// Shorter means lower latency but more idle DB queries.
	pollInterval time.Duration
}

// NewProcessor creates a Processor. pollyClient is initialised here
// so the Polly dependency is explicit — main.go passes in config values
// rather than the processor reading environment variables itself.
func NewProcessor(
    bookRepo *repository.BookRepository,
    auditRepo *repository.AuditRepository,
    store storage.Storage,
    elevenLabsKey, elevenLabsVoiceID string,
    workerCount, pollSecs int,
) (*Processor, error) {
    return &Processor{
        bookRepo:          bookRepo,
        auditRepo:         auditRepo,
        store:             store,
        elevenLabsKey:     elevenLabsKey,
        elevenLabsVoiceID: elevenLabsVoiceID,
        workerCount:       workerCount,
        pollInterval:      time.Duration(pollSecs) * time.Second,
    }, nil
}

// Start launches workerCount goroutines and returns immediately.
// ctx controls the lifetime of all workers — cancel it to shut them
// down gracefully (e.g. on SIGTERM in main.go).
// Call this once after initialisation, not once per request.
func (p *Processor) Start(ctx context.Context) {
	for i := range p.workerCount {
		// Each worker gets its own index for logging so you can tell
		// which worker processed which book in the logs.
		go p.runWorker(ctx, i+1)
	}

	log.Printf("processor: started %d workers (poll interval: %s)", p.workerCount, p.pollInterval)
}

// =============================================================
// WORKER LOOP
// Each worker runs this loop independently for its entire lifetime.
// =============================================================

// runWorker is the main loop for a single processor goroutine.
// It claims one book at a time, processes it, then immediately
// looks for the next one. When the queue is empty it sleeps for
// pollInterval before trying again, to avoid hammering the DB.
func (p *Processor) runWorker(ctx context.Context, workerID int) {
	log.Printf("processor: worker %d started", workerID)

	for {
		// Check for shutdown before every iteration so workers exit
		// promptly when the server is stopping, rather than after
		// finishing a potentially long Polly call.
		select {
		case <-ctx.Done():
			log.Printf("processor: worker %d shutting down", workerID)
			return
		default:
		}

		processed, err := p.claimAndProcess(ctx, workerID)
		if err != nil {
			// Log but do not exit — a transient error (network blip,
			// Polly timeout) should not kill the worker permanently.
			log.Printf("processor: worker %d error: %v", workerID, err)
		}

		if !processed {
			// Queue was empty — sleep before polling again so we do not
			// issue a DB query every millisecond when there is nothing to do.
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
// The core unit of work — claim one book, process it end to end.
// =============================================================

// claimAndProcess attempts to claim and fully process one pending book.
// Returns (true, nil) on success, (false, nil) when the queue is empty,
// and (false, err) when something went wrong.
func (p *Processor) claimAndProcess(ctx context.Context, workerID int) (bool, error) {
	// ClaimNextPending uses SELECT FOR UPDATE SKIP LOCKED — it returns
	// a locked row and an open transaction. We must commit or roll back
	// that transaction before this function returns.
	book, tx, err := p.bookRepo.ClaimNextPending(ctx)
	if err != nil {
		return false, fmt.Errorf("claim pending book: %w", err)
	}
	if book == nil {
		// No pending books — signal the caller to sleep.
		return false, nil
	}

	log.Printf("processor: worker %d claimed book %s (%s)", workerID, book.ID, book.Title)

	// processBook does all the work. If it fails we mark the book as
	// failed and roll back the claim transaction.
	audioPath, err := p.processBook(ctx, book)
	if err != nil {
		// Roll back the claim lock — the book row goes back to its
		// original state so the failure update below can proceed cleanly.
		tx.Rollback()

		log.Printf("processor: worker %d failed book %s: %v", workerID, book.ID, err)
		p.markFailed(ctx, book, err.Error())

		// Return nil error — the failure is recorded, the worker should
		// continue running rather than treating this as a fatal problem.
		return true, nil
	}

	// Processing succeeded — update the book row within the claim
	// transaction so the lock and the status update are atomic.
	if err := p.bookRepo.UpdateAudioReady(ctx, tx, book.ID, audioPath, book.Version); err != nil {
		tx.Rollback()
		p.markFailed(ctx, book, fmt.Sprintf("update audio ready: %v", err))
		return true, nil
	}

	// Commit atomically releases the row lock and saves the status update.
	if err := tx.Commit(); err != nil {
		p.markFailed(ctx, book, fmt.Sprintf("commit transaction: %v", err))
		return true, nil
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

// processBook runs the three processing steps in order.
// Returning an error from any step skips the remaining steps and
// causes the caller to mark the book as failed.
func (p *Processor) processBook(ctx context.Context, book *models.Book) (string, error) {
	// Step 1: retrieve the PDF from storage.
	pdfData, err := p.fetchPDF(ctx, book)
	if err != nil {
		return "", fmt.Errorf("fetch pdf: %w", err)
	}

	// Step 2: extract plain text from the PDF bytes.
	text, err := extractText(pdfData)
	if err != nil {
		return "", fmt.Errorf("extract text: %w", err)
	}

	// Step 3: synthesise audio from the extracted text.
	audioData, err := p.synthesise(ctx, text)
	if err != nil {
		return "", fmt.Errorf("synthesise audio: %w", err)
	}

	// Step 4: save the audio to storage and return the key.
	audioKey := storage.AudioKey(book.ID)
	if err := p.store.Save(ctx, audioKey, audioData, "audio/mpeg"); err != nil {
		return "", fmt.Errorf("save audio: %w", err)
	}

	return audioKey, nil
}

// =============================================================
// INDIVIDUAL PROCESSING STEPS
// Each step is its own function so the failure point is obvious
// in logs and each can be tested independently.
// =============================================================

// fetchPDF retrieves the raw PDF bytes from storage.
// PDFPath is a pointer because it is nullable in the DB — we validate
// it is set before attempting the fetch.
func (p *Processor) fetchPDF(ctx context.Context, book *models.Book) ([]byte, error) {
	if book.PDFPath == nil || *book.PDFPath == "" {
		return nil, fmt.Errorf("book has no pdf_path")
	}

	// Reads PDF bytes back from whichever storage backend is active.
	// LocalStorage reads from disk, S3Storage calls GetObject.
	data, err := readFromStore(ctx, p.store, *book.PDFPath)
	if err != nil {
		return nil, fmt.Errorf("read pdf from storage: %w", err)
	}

	return data, nil
}

// extractText pulls plain text out of PDF bytes page by page.
// Pages that fail to parse are skipped with a warning rather than
// aborting — a partially extracted document is better than nothing.
func extractText(pdfData []byte) (string, error) {
	reader := bytes.NewReader(pdfData)

	pdfReader, err := pdf.NewReader(reader, int64(len(pdfData)))
	if err != nil {
		return "", fmt.Errorf("open pdf reader: %w", err)
	}

	var sb strings.Builder
	totalPages := pdfReader.NumPage()

	for pageNum := 1; pageNum <= totalPages; pageNum++ {
		page := pdfReader.Page(pageNum)
		if page.V.IsNull() {
			continue
		}

		text, err := page.GetPlainText(nil)
		if err != nil {
			// A single bad page should not abort the whole book.
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

// synthesise converts text to MP3 audio using ElevenLabs.
// Text is split into chunks to stay within API limits.
// Each chunk is synthesised independently and the MP3 bytes
// are concatenated — ElevenLabs produces clean joins unlike Polly.
func (p *Processor) synthesise(ctx context.Context, text string) ([]byte, error) {
    const maxChunkSize = 2500 // ElevenLabs handles up to 5000 but smaller = faster response

    chunks := splitIntoChunks(text, maxChunkSize)
    log.Printf("processor: synthesising %d chunk(s) via elevenlabs", len(chunks))

    var fullAudio []byte

    for i, chunk := range chunks {
        audio, err := p.elevenLabsTTS(ctx, chunk)
        if err != nil {
            return nil, fmt.Errorf("elevenlabs chunk %d of %d: %w", i+1, len(chunks), err)
        }
        fullAudio = append(fullAudio, audio...)
    }

    return fullAudio, nil
}

// elevenLabsTTS calls the ElevenLabs API to convert a single text chunk
// to MP3 audio. Returns raw MP3 bytes ready to concatenate or upload.
func (p *Processor) elevenLabsTTS(ctx context.Context, text string) ([]byte, error) {
    body, err := json.Marshal(map[string]any{
        "text":     text,
        "model_id": "eleven_turbo_v2_5", // latest turbo — fast, cheap, high quality
        "voice_settings": map[string]any{
            "stability":        0.5,  // 0=very expressive, 1=very consistent
            "similarity_boost": 0.75, // how closely to match the original voice
            "style":            0.0,  // keep at 0 for clearest educational reading
            "use_speaker_boost": true,
        },
    })
    if err != nil {
        return nil, fmt.Errorf("marshal request: %w", err)
    }

    url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s", p.elevenLabsVoiceID)
    req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("create request: %w", err)
    }

    req.Header.Set("xi-api-key",   p.elevenLabsKey)
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Accept",       "audio/mpeg")

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



// splitIntoChunks breaks text into slices of at most maxSize characters.
// It splits on word boundaries where possible to avoid cutting words in
// the middle, which would produce unnatural audio.
func splitIntoChunks(text string, maxSize int) []string {
	if len(text) <= maxSize {
		return []string{text}
	}

	var chunks []string
	words := strings.Fields(text)
	var current strings.Builder

	for _, word := range words {
		// +1 accounts for the space between words.
		if current.Len()+len(word)+1 > maxSize {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
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

// =============================================================
// FAILURE HANDLING
// =============================================================

// markFailed updates the book status to 'failed' and writes an audit
// log entry with the reason. Called whenever any processing step fails.
// Uses a fresh background context — the original ctx may be cancelled
// if the failure was caused by a shutdown signal.
func (p *Processor) markFailed(ctx context.Context, book *models.Book, reason string) {
	// Use a short-lived independent context so we can still write the
	// failure even if the original request context was cancelled.
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

// =============================================================
// STORAGE READ HELPER
// LocalStorage only exposes Save and URL — reading a file back
// requires going to disk directly. For S3, you would add a Get
// method to the Storage interface and use that here instead.
// =============================================================

// readFromStore reads a file from local disk storage by its key.
// This is only used by the processor — nothing else needs to read
// raw file bytes back out of storage.
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