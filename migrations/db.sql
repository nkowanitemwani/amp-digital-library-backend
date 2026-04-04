-- Active: 1754677062822@@127.0.0.1@5432@amplifydigitallibrary

CREATE DATABASE amplifydigitallibrary;

-- ============================================================
-- EXTENSIONS
-- ============================================================
CREATE EXTENSION IF NOT EXISTS "pgcrypto";  -- gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS "citext";    -- case-insensitive text


-- ============================================================
-- SCHOOLS
-- ============================================================

-- Tracks school accounts. Each school is an independent tenant.
-- The admin logs in with email + password and manages grades,
-- categories, and books for their school.
CREATE TABLE schools (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT        NOT NULL CHECK (char_length(name) BETWEEN 2 AND 100),
    email           CITEXT      NOT NULL UNIQUE,-- CITEXT: stored as-is, compared case-insensitively,Prevents duplicate accounts like "admin@school.com" vs "Admin@School.com".
    password_hash   TEXT        NOT NULL,
    location        TEXT        CHECK (char_length(location) <= 200),
    is_active       BOOLEAN     NOT NULL DEFAULT true,-- Soft disable: suspend a school without destroying their data
    login_attempts  INTEGER     NOT NULL DEFAULT 0 CHECK (login_attempts >= 0),-- Brute force protection: incremented on each failed login,Reset to 0 on successful login.
    locked_until    TIMESTAMPTZ,-- Set to a future timestamp after too many failed attempts,App checks this before attempting any login.
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);


-- ============================================================
-- GRADES
-- ============================================================

-- A grade is a shared login account for an entire class.
-- e.g. "Grade 3" has one username/password shared by all students
-- in that class — they log in simultaneously in a computer lab.
-- Categories and books are scoped to a grade, not a school, so
-- Grade 3 students only see Grade 3 content.
--
-- username is a simple handle set by the admin, e.g. "grade3".
-- It is unique within a school — two schools can both have "grade3".
CREATE TABLE grades (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    school_id       UUID        NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
    name            TEXT        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),-- Display name shown in the admin dashboard, e.g. "Grade 3".
    username        CITEXT      NOT NULL CHECK (char_length(username) BETWEEN 2 AND 50),-- Login handle used by students, e.g. "grade3",CITEXT so "Grade3" and "grade3" are treated as the same username.
    password_hash   TEXT        NOT NULL,
    is_active       BOOLEAN     NOT NULL DEFAULT true,-- Soft disable: deactivate a grade without deleting its books.
    login_attempts  INTEGER     NOT NULL DEFAULT 0 CHECK (login_attempts >= 0),-- Brute force protection — shared counters mean all simultaneous,logins from the computer lab share the same lockout state.
    locked_until    TIMESTAMPTZ,
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_grade_username_per_school UNIQUE (school_id, username),-- A username must be unique within a school.
    CONSTRAINT uq_grade_id_school UNIQUE (id, school_id)-- Required to support the composite FK on categories,Ensures a category always belongs to the same school as its grade.
);


-- ============================================================
-- CATEGORIES
-- ============================================================

-- Each grade defines its own categories (e.g. "Science", "Maths").
-- Scoped to a grade — Grade 3 and Grade 4 have independent category lists.
-- CITEXT on name prevents "Science" and "science" from coexisting
-- within the same grade.
CREATE TABLE categories (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    school_id   UUID        NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
    grade_id    UUID        NOT NULL REFERENCES grades(id)  ON DELETE CASCADE,
    name        CITEXT      NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_category_per_grade UNIQUE (grade_id, name),-- No duplicate category names within a single grade.
    CONSTRAINT uq_category_id_grade UNIQUE (id, grade_id),-- Required to support the composite FK on books,Ensures a category belongs to the same grade as its books.
    CONSTRAINT fk_category_grade_school
        FOREIGN KEY (school_id, grade_id)
        REFERENCES grades (school_id, id)-- Composite FK: ensures the category's grade belongs to the same school,Structurally prevents cross-school data at the DB layer.
);


-- ============================================================
-- BOOKS
-- ============================================================

-- Enum enforces valid status values at the DB level.
-- A typo in application code is rejected immediately.
CREATE TYPE book_status AS ENUM ('processing', 'ready', 'failed');
ALTER TYPE book_status ADD VALUE 'pending';

-- Each book belongs to a grade and a category within that grade.
-- The composite FK (grade_id, category_id) makes it structurally
-- impossible for a book to reference a category from a different grade.
CREATE TABLE books (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    school_id   UUID        NOT NULL REFERENCES schools(id)    ON DELETE CASCADE,
    grade_id    UUID        NOT NULL REFERENCES grades(id)     ON DELETE CASCADE,
    category_id UUID        NOT NULL REFERENCES categories(id) ON DELETE RESTRICT,
    title       TEXT        NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    author      TEXT        CHECK (char_length(author) <= 100),
    unit_number INTEGER     NOT NULL CHECK (unit_number > 0),-- Determines the listening order within a category.
    pdf_path    TEXT,-- Storage keys (S3 path or local path).
    audio_path  TEXT,-- pdf_path is set on upload. audio_path is set after processing completes.
    status      book_status NOT NULL DEFAULT 'processing',
    version     INTEGER     NOT NULL DEFAULT 1 CHECK (version > 0),-- Optimistic locking: incremented on every update,Prevents two concurrent processes from overwriting each other's changes,Update pattern: WHERE id = $1 AND version = $2 → check rowsAffected = 1.
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_unit_per_category UNIQUE (category_id, unit_number),-- No duplicate unit numbers within a single category.
    CONSTRAINT fk_book_category_grade
        FOREIGN KEY (grade_id, category_id)
        REFERENCES categories (grade_id, id)-- Composite FK: ensures this book's category belongs to the same grade,Catches cross-grade data leaks at the DB layer independently of anything the application layer does.
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
    action      TEXT        NOT NULL,   -- e.g. 'book.created', 'grade.login.failed'
    entity      TEXT,                   -- e.g. 'book', 'category', 'grade'
    entity_id   UUID,                   -- the ID of the affected row
    metadata    JSONB,                  -- flexible extra context
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);


-- ============================================================
-- INDEXES
-- ============================================================

-- List all grades for a school (admin dashboard primary path).
CREATE INDEX idx_grades_school_id
    ON grades(school_id);

-- List all categories for a grade (primary browse path).
CREATE INDEX idx_categories_grade_id
    ON categories(grade_id);

-- Admin dashboard: list all categories in a school across all grades.
CREATE INDEX idx_categories_school_id
    ON categories(school_id);

-- List all books in a category, ordered by unit number (primary student path).
CREATE INDEX idx_books_category_id
    ON books(category_id);

-- Admin dashboard: list all books belonging to a grade.
CREATE INDEX idx_books_grade_id
    ON books(grade_id);

-- Processor queue: find books still needing processing.
-- Partial index stays small because most books will be 'ready'.
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

CREATE TRIGGER trg_grades_updated_at
    BEFORE UPDATE ON grades
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_categories_updated_at
    BEFORE UPDATE ON categories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_books_updated_at
    BEFORE UPDATE ON books
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();