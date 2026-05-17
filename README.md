# Amplify Digital Library — Backend API

> Cloud-based accessible digital library backend for Zambian primary schools. Converts PDF textbooks into audio so visually impaired students can access the same curriculum as their classmates.

Built with **Go 1.22 · Gin · PostgreSQL · AWS S3 · ElevenLabs · Groq**

---

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Project Structure](#project-structure)
- [Prerequisites](#prerequisites)
- [Local Development](#local-development)
- [Environment Variables](#environment-variables)
- [Database](#database)
- [API Overview](#api-overview)
- [Deployment](#deployment)
- [Background Processor](#background-processor)
- [Known Limitations](#known-limitations)

---

## Overview

Amplify is a capstone project built at Mulungushi University, Zambia. The system allows school administrators to upload PDF textbooks, which are automatically converted into audio lessons and dialogue-style discussions using AI. Students access the audio library through a shared grade account on any school computer.

**Core capabilities:**
- PDF textbook upload and management
- Automatic audio generation via ElevenLabs (neural TTS)
- AI-generated dialogue and quiz content via Groq LLM
- JWT-based authentication with admin and grade-level roles
- Quiz attempts with scoring and review
- Audit logging for all administrative actions

---

## Architecture

```
Next.js Frontend (Vercel)
        │
        │ HTTPS
        ▼
Go/Gin REST API (Render)
        │
        ├──► PostgreSQL (Render Managed DB)
        ├──► AWS S3 (PDF + audio file storage)
        ├──► ElevenLabs API (text-to-speech)
        └──► Groq API (LLM — dialogue + quiz generation)
```

The backend is a single Go process that runs both the HTTP server and background processing goroutines. All file assets (PDFs and generated MP3s) are stored in AWS S3 and served via presigned URLs — the API server never proxies file content.

---

## Project Structure

```
.
├── cmd/
│   └── server/
│       └── main.go               # Entry point — wires everything, starts server
├── internal/
│   ├── config/
│   │   └── config.go             # Reads all env vars into a Config struct
│   ├── db/
│   │   └── db.go                 # PostgreSQL connection pool (database/sql + lib/pq)
│   ├── models/
│   │   └── models.go             # Domain structs and request/response types
│   ├── repository/               # All SQL queries
│   │   ├── school_repo.go
│   │   ├── grade_repo.go
│   │   ├── category_repo.go
│   │   ├── book_repo.go
│   │   ├── question_repo.go
│   │   ├── attempt_repo.go
│   │   └── audit_repo.go
│   ├── service/                  # Business logic
│   │   ├── school_service.go
│   │   ├── grade_service.go
│   │   ├── category_service.go
│   │   ├── book_service.go
│   │   ├── quiz_service.go
│   │   └── processor.go          # Background book processing worker
│   ├── handler/                  # HTTP handlers (Gin)
│   │   ├── school_handler.go
│   │   ├── grade_handler.go
│   │   ├── category_handler.go
│   │   ├── book_handler.go
│   │   └── quiz_handler.go
│   └── middleware/
│       └── auth.go               # JWT validation, RequireAuth, RequireAdmin, RequireGrade
├── storage/
│   └── storage.go                # Storage interface — LocalStorage and S3Storage implementations
├── migrations/
│   └── db.sql                    # Full database schema
└── go.mod
```

---

## Prerequisites

- Go 1.22+
- PostgreSQL 14+
- An AWS account with an S3 bucket and IAM credentials
- An ElevenLabs account with API key and two voice IDs
- A Groq account with API key

---

## Local Development

**1. Clone the repository**

```bash
git https://github.com/nkowanitemwani/amp-digital-library-backend
cd your-repo-name
```

**2. Install dependencies**

```bash
go mod download
```

**3. Create a local PostgreSQL database**

```bash
createdb amplify_local
psql amplify_local -f migrations/db.sql
```

Enable required extensions (if not already enabled):

```bash
psql amplify_local -c "CREATE EXTENSION IF NOT EXISTS pgcrypto;"
psql amplify_local -c "CREATE EXTENSION IF NOT EXISTS citext;"
```

**4. Copy and fill in environment variables**

```bash
cp .env.example .env
# Edit .env with your values
```

**5. Run the server**

```bash
go run ./cmd/server
```

The API will be available at `http://localhost:8080`.

---

## Environment Variables

| Variable | Description | Required |
|---|---|---|
| `SERVER_PORT` | HTTP port to listen on (default: `8080`) | No |
| `JWT_SECRET` | Secret key for signing JWT tokens | **Yes** |
| `DATABASE_URL` | Full PostgreSQL DSN — used on Render | Either this... |
| `DB_HOST` | PostgreSQL host | ...or these four |
| `DB_PORT` | PostgreSQL port (default: `5432`) | |
| `DB_USER` | PostgreSQL username | |
| `DB_PASSWORD` | PostgreSQL password | |
| `DB_NAME` | PostgreSQL database name | |
| `AWS_REGION` | AWS region of your S3 bucket (e.g. `us-east-1`) | **Yes** |
| `AWS_ACCESS_KEY_ID` | AWS IAM access key | **Yes** |
| `AWS_SECRET_ACCESS_KEY` | AWS IAM secret key | **Yes** |
| `S3_BUCKET_NAME` | Name of the S3 bucket for file storage | **Yes** |
| `ELEVENLABS_API_KEY` | ElevenLabs API key | **Yes** |
| `ELEVENLABS_VOICE_A` | ElevenLabs voice ID for narrator voice | **Yes** |
| `ELEVENLABS_VOICE_B` | ElevenLabs voice ID for dialogue second voice | **Yes** |
| `GROQ_API_KEY` | Groq API key for LLM inference | **Yes** |
| `PROCESSOR_WORKERS` | Number of background processing goroutines (default: `3`) | No |
| `PROCESSOR_POLL_SECS` | How often the processor polls for new books in seconds (default: `5`) | No |
| `ENVIRONMENT` | Set to `production` on Render to disable local file serving | No |

> Generate a strong JWT secret with: `openssl rand -hex 32`

---

## Database

The schema is in `migrations/db.sql`. Tables:

| Table | Description |
|---|---|
| `schools` | Registered schools (each with a unique school ID) |
| `grades` | Grade-level accounts belonging to a school |
| `categories` | Subject categories (e.g. Mathematics, Science) |
| `books` | Uploaded textbooks and their processing status |
| `questions` | AI-generated quiz questions per book |
| `quiz_attempts` | Student quiz submissions and scores |
| `audit_log` | Admin action log |

**Book processing states:** `pending` → `processing` → `ready` (or `failed`)

---

## API Overview

All routes are prefixed. Authentication uses `Authorization: Bearer <token>` headers.

**Auth**
- `POST /auth/login` — Admin login (email + password)
- `POST /auth/grade/login` — Grade/student login (school ID + username + password)
- `POST /auth/register` — Register a new school

**Admin routes** (require admin JWT)
- `GET/POST /admin/schools` — List and manage schools
- `GET/POST /admin/grades` — Manage grade accounts
- `GET/POST /admin/categories` — Manage subject categories
- `GET/POST /admin/books` — Upload and manage books
- `GET /admin/books/:id/status` — Check processing status

**Student routes** (require grade JWT)
- `GET /student/books` — List available books for the student's grade
- `GET /student/books/:id/audio` — Get presigned S3 URL for standard audio
- `GET /student/books/:id/dialogue` — Get presigned S3 URL for dialogue audio
- `GET /student/books/:id/questions` — Get quiz questions for a book
- `POST /student/books/:id/attempts` — Submit a quiz attempt

---

## Deployment

The backend is deployed on **Render** using the native Go runtime.

| Field | Value |
|---|---|
| **Runtime** | Go (auto-detected from `go.mod`) |
| **Build Command** | `go build -o server ./cmd/server` |
| **Start Command** | `./server` |
| **Region** | Oregon (US WESt) |

The managed PostgreSQL database is also provisioned on Render in the same region. The `DATABASE_URL` environment variable is set in the Render dashboard and is read automatically by the application.


---

## Background Processor

The book processor runs as goroutines inside the main server process. After a book is uploaded:

1. Its status is set to `processing` in the database
2. The processor picks it up on the next poll cycle
3. The PDF text is extracted and sent to **Groq** to generate dialogue and quiz scripts
4. The scripts are sent to **ElevenLabs** to generate MP3 audio files
5. The audio files are uploaded to **AWS S3**
6. The book status is updated to `ready`

Worker count and poll interval are configurable via `PROCESSOR_WORKERS` and `PROCESSOR_POLL_SECS`.

---

## Known Limitations

- **Processor and HTTP server share one process.** On the free Render tier, the server spins down after 15 minutes of inactivity, which can interrupt active processing jobs. In a production architecture, the processor would run as a separate Background Worker service.

- **Free PostgreSQL expires after 90 days** on Render's free tier. Upgrade to a paid plan for persistent production use.

- **CORS is currently open.** Restrict `FRONTEND_URL` in production by setting the environment variable to your Vercel deployment URL.

- **No job queue.** Processing jobs are tracked via database status fields rather than a dedicated queue (e.g. Redis). This is sufficient for an MVP but limits retry logic and observability.

---

## Licence

This project was developed as a final-year capstone project at Mulungushi University, Zambia.