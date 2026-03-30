-- Active: 1754677062822@@127.0.0.1@5432@ampdigitallibrary
CREATE DATABASE ampDigitalLibrary;

CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "citext";        --case insensitive text  stores as-is but compares case-insensitively at the database level. "Science" and "science" won't create duplicate categories, and "Admin@School.com" won't bypass a login check

--TABLES--

--1.SCHOOLS--

CREATE TABLE schools (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 2 AND 100),
    email CITEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    location TEXT CHECK (char_length(location) <= 200),
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

--2.CATEGORIES--

CREATE TABLE categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
    name CITEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT up_category_per_school UNIQUE (school_id, name)
);

--3.BOOKS--

CREATE TYPE book_status AS ENUM ('processing','ready','failed'); --the database itself rejects any value outside processing | ready | failed. A typo in application code ("proccessing") fails loudly at the DB boundary rather than silently corrupting data.

CREATE TABLE books (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    school_id    UUID        NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
    category_id  UUID        NOT NULL REFERENCES categories(id) ON DELETE RESTRICT, --Deleting a category is RESTRICT — it refuses if books still exist in it, forcing the admin to move or delete books first. This prevents accidental data loss from a single category delete.
    title        TEXT        NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    author       TEXT        CHECK (char_length(author) <= 100),
    unit_number  INTEGER     NOT NULL CHECK (unit_number > 0),
    pdf_path     TEXT,
    audio_path   TEXT,
    status       book_status NOT NULL DEFAULT 'processing',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_unit_per_category UNIQUE (category_id, unit_number),
    CONSTRAINT fk_book_category_school FOREIGN KEY (school_id, category_id) --  It makes it structurally impossible for a book to reference a category belonging to a different school
        REFERENCES categories(school_id, id)          --A book MUST belong to the same school as its category
);

-- Required for the composite FK above to resolve
ALTER TABLE categories
    ADD CONSTRAINT uq_category_id_school UNIQUE (id, school_id);


--4.AUDIT LOG

CREATE TABLE audit_log (
    id          BIGSERIAL   PRIMARY KEY, --BIGSERIAL not UUID here — audit rows are always read sequentially, so an incrementing integer is better for index performance.
    school_id   UUID        REFERENCES schools(id) ON DELETE SET NULL,
    action      TEXT        NOT NULL,   
    entity      TEXT,                   
    entity_id   UUID,
    metadata    JSONB,                  
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

--5.INDEXES--
-- Primary access pattern: a school browses its own categories
CREATE INDEX idx_categories_school_id ON categories(school_id);

-- Primary access pattern: list books in a category,
CREATE INDEX idx_books_category_id    ON books(category_id);
CREATE INDEX idx_books_school_id      ON books(school_id);

-- Dashboard: admin checks which books are still processing
CREATE INDEX idx_books_status         ON books(status) WHERE status != 'ready';

-- Audit queries: "shows all actions performed by a school"
CREATE INDEX idx_audit_school_id      ON audit_log(school_id);
CREATE INDEX idx_audit_created_at     ON audit_log(created_at DESC);

--6.AUTO UPDATE TRIGGERS--

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

-- 
--What updated_at is actually used for in practice:
--Debugging — "when did this book's status last change?"
--Cache invalidation — clients can ask "give me all books updated since timestamp X" instead of fetching everything
--Audit trail — a lightweight complement to the audit_log table, visible directly on the row
--Sync — if you ever replicate data to another system, updated_at is how you know what changed
--