CREATE TABLE reviews (
    id             BIGSERIAL PRIMARY KEY,
    repo           TEXT NOT NULL,
    pr_number      INT NOT NULL,
    head_sha       TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    findings_count INT NOT NULL,
    latency_ms     BIGINT NOT NULL
);

CREATE INDEX idx_reviews_repo_pr ON reviews (repo, pr_number);

CREATE TABLE findings (
    id        BIGSERIAL PRIMARY KEY,
    review_id BIGINT NOT NULL REFERENCES reviews(id) ON DELETE CASCADE,
    file      TEXT NOT NULL,
    line      INT NOT NULL,
    severity  TEXT NOT NULL,
    category  TEXT NOT NULL,
    message   TEXT NOT NULL,
    feedback  TEXT
);

CREATE INDEX idx_findings_review_id ON findings (review_id);
