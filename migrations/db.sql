-- Active: 1754677062822@@127.0.0.1@5432@ampdigitallibrary
CREATE DATABASE ampdigitallibrary;


-- ============================================================
-- EXTENSIONS
-- ============================================================
CREATE EXTENSION IF NOT EXISTS "pgcrypto";  -- gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS "citext";    -- case-insensitive text


-- ============================================================
-- SCHOOLS
-- ============================================================

-- Tracks school accounts. Each school is an independent tenant —
-- all categories and books are scoped to a school_id.
-- Login tracking fields (login_attempts, locked_until) handle
-- brute force at the DB level, shared across all app instances.
CREATE TABLE schools (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT        NOT NULL CHECK (char_length(name) BETWEEN 2 AND 100),
    email           CITEXT      NOT NULL UNIQUE, -- CITEXT: stored as-is, compared case-insensitively,Prevents duplicate accounts like "admin@school.com" vs "Admin@School.com".
    password_hash   TEXT        NOT NULL,
    location        TEXT        CHECK (char_length(location) <= 200),
    is_active       BOOLEAN     NOT NULL DEFAULT true,
    login_attempts  INTEGER     NOT NULL DEFAULT 0 CHECK (login_attempts >= 0), -- Brute force protection: incremented on each failed login,Reset to 0 on successful login.
    locked_until    TIMESTAMPTZ,
    last_login_at   TIMESTAMPTZ,    -- Set to a future timestamp after too many failed attempts,App checks this before attempting any login.
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);


-- ============================================================
-- CATEGORIES
-- ============================================================

-- Each school defines their own categories (e.g. "Science", "Maths").
-- CITEXT on name prevents "Science" and "science" from coexisting within the same school.
CREATE TABLE categories (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    school_id   UUID        NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
    name        CITEXT      NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_category_per_school UNIQUE (school_id, name), -- No duplicate category names within a single school.
    CONSTRAINT uq_category_id_school  UNIQUE (id, school_id) -- Required to support the composite FK on books,Allows books to enforce that their category belongs to the same school.
);


-- ============================================================
-- BOOKS
-- ============================================================

-- Enum enforces valid status values at the DB level
-- A typo in application code is rejected immediately.
CREATE TYPE book_status AS ENUM ('processing', 'ready', 'failed');

-- Each book belongs to a school and a category.
-- The composite FK (school_id, category_id) → categories(id, school_id)
-- makes it structurally impossible for a book to reference a category
-- from a different school, even if there is a bug in the service layer.
CREATE TABLE books (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    school_id   UUID        NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
    category_id UUID        NOT NULL REFERENCES categories(id) ON DELETE RESTRICT,
    title       TEXT        NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    author      TEXT        CHECK (char_length(author) <= 100),
    unit_number INTEGER     NOT NULL CHECK (unit_number > 0), -- Determines the listening order within a category.
    pdf_path    TEXT, -- set on upload
    audio_path  TEXT, --set after processing completes.
    status      book_status NOT NULL DEFAULT 'processing',
    version     INTEGER     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_unit_per_category UNIQUE (category_id, unit_number), -- No duplicate unit numbers within a single category.
    CONSTRAINT fk_book_category_school
        FOREIGN KEY (school_id, category_id)
        REFERENCES categories (school_id, id) -- Composite FK: enforces that this book's category belongs to the same school. Catches cross-school data leaks at the DB layer.
);


-- ============================================================
-- AUDIT LOG
-- ============================================================

-- Append-only record of significant actions.
-- Rows are never updated or deleted — only inserted.
-- BIGSERIAL (not UUID) because rows are always read sequentially
-- and an incrementing integer performs better for that access pattern.
-- JSONB metadata allows attaching arbitrary context (IP, filename, etc.)
-- without ever changing this table's schema.
CREATE TABLE audit_log (
    id          BIGSERIAL   PRIMARY KEY,
    school_id   UUID        REFERENCES schools(id) ON DELETE SET NULL,
    action      TEXT        NOT NULL,   -- e.g. 'book.created', 'school.login.failed'
    entity      TEXT,                   -- e.g. 'book', 'category', 'school'
    entity_id   UUID,                   -- the ID of the affected row
    metadata    JSONB,                  -- flexible extra context
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);


-- ============================================================
-- INDEXES
-- ============================================================

-- School lookups by email (login path).
-- CITEXT already creates a case-insensitive index via the UNIQUE constraint,
-- so no extra index is needed here.

-- List all categories for a school (primary browse path).
CREATE INDEX idx_categories_school_id
    ON categories(school_id);

-- List all books in a category, ordered by unit number (primary student path).
CREATE INDEX idx_books_category_id
    ON books(category_id);

-- Admin dashboard: list all books belonging to a school.
CREATE INDEX idx_books_school_id
    ON books(school_id);

-- Processor queue: find books that still need processing.
-- Partial index (WHERE status != 'ready') stays small as the library grows
-- because the vast majority of books will be 'ready'.
CREATE INDEX idx_books_pending
    ON books(created_at)
    WHERE status != 'ready';

-- Audit queries: all actions by a school, most recent first.
CREATE INDEX idx_audit_school_id
    ON audit_log(school_id, created_at DESC);


-- ============================================================
-- updated_at TRIGGER
-- ============================================================

-- Single shared function — one trigger per table calls it.
-- Enforced at the DB level so updated_at cannot be forgotten
-- or bypassed by any application path.
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_schools_updated_at
    BEFORE UPDATE ON schools
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_categories_updated_at
    BEFORE UPDATE ON categories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_books_updated_at
    BEFORE UPDATE ON books
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();