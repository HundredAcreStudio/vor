-- +goose Up
-- +goose StatementBegin

-- PostgreSQL variant of the consolidated end-state schema. Mirrors the SQLite
-- version in internal/persistence/migrations/sqlite/0001_init.sql with these
-- differences:
--   - TIMESTAMPTZ for time columns (vs SQLite DATETIME)
--   - BOOLEAN for true/false columns (vs SQLite INTEGER 0/1)
--   - BIGSERIAL for auto-increment integer PKs
--   - pgvector embedding column on wiki_pages
--   - GIN tsvector index in place of FTS5 virtual table

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS repositories (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    url             TEXT NOT NULL DEFAULT '',
    local_path      TEXT NOT NULL,
    default_branch  TEXT NOT NULL DEFAULT 'main',
    head_commit     TEXT,
    settings_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS generation_jobs (
    id               TEXT PRIMARY KEY,
    repository_id    TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    status           TEXT NOT NULL DEFAULT 'pending',
    provider_name    TEXT NOT NULL DEFAULT '',
    model_name       TEXT NOT NULL DEFAULT '',
    total_pages      INTEGER NOT NULL DEFAULT 0,
    completed_pages  INTEGER NOT NULL DEFAULT 0,
    failed_pages     INTEGER NOT NULL DEFAULT 0,
    current_level    INTEGER NOT NULL DEFAULT 0,
    error_message    TEXT,
    config_json      TEXT NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS wiki_pages (
    id                TEXT PRIMARY KEY,
    repository_id     TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    page_type         TEXT NOT NULL,
    title             TEXT NOT NULL,
    content           TEXT NOT NULL,
    summary           TEXT NOT NULL DEFAULT '',
    target_path       TEXT NOT NULL,
    source_hash       TEXT NOT NULL,
    model_name        TEXT NOT NULL,
    provider_name     TEXT NOT NULL,
    input_tokens      INTEGER NOT NULL DEFAULT 0,
    output_tokens     INTEGER NOT NULL DEFAULT 0,
    cached_tokens     INTEGER NOT NULL DEFAULT 0,
    generation_level  INTEGER NOT NULL DEFAULT 0,
    version           INTEGER NOT NULL DEFAULT 1,
    confidence        DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    freshness_status  TEXT NOT NULL DEFAULT 'fresh',
    metadata_json     TEXT NOT NULL DEFAULT '{}',
    human_notes       TEXT,
    embedding         vector(1536),
    created_at        TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_wiki_pages_fts
    ON wiki_pages
    USING GIN (to_tsvector('english', COALESCE(title, '') || ' ' || COALESCE(content, '')));

CREATE TABLE IF NOT EXISTS wiki_page_versions (
    id             TEXT PRIMARY KEY,
    page_id        TEXT NOT NULL REFERENCES wiki_pages(id),
    repository_id  TEXT NOT NULL,
    version        INTEGER NOT NULL,
    page_type      TEXT NOT NULL,
    title          TEXT NOT NULL,
    content        TEXT NOT NULL,
    source_hash    TEXT NOT NULL,
    model_name     TEXT NOT NULL,
    provider_name  TEXT NOT NULL,
    input_tokens   INTEGER NOT NULL DEFAULT 0,
    output_tokens  INTEGER NOT NULL DEFAULT 0,
    confidence     DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    archived_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS external_systems (
    id             BIGSERIAL PRIMARY KEY,
    repository_id  TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    display_name   TEXT NOT NULL DEFAULT '',
    ecosystem      TEXT NOT NULL,
    category       TEXT NOT NULL DEFAULT 'library',
    version        TEXT,
    declared_in    TEXT NOT NULL,
    is_dev_dep     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_external_system ON external_systems(repository_id, name, declared_in);
CREATE INDEX IF NOT EXISTS ix_external_systems_repository_id ON external_systems(repository_id);

CREATE TABLE IF NOT EXISTS graph_nodes (
    id                   TEXT PRIMARY KEY,
    repository_id        TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    node_id              TEXT NOT NULL,
    node_type            TEXT NOT NULL DEFAULT 'file',
    language             TEXT NOT NULL DEFAULT '',
    symbol_count         INTEGER NOT NULL DEFAULT 0,
    has_error            BOOLEAN NOT NULL DEFAULT FALSE,
    is_test              BOOLEAN NOT NULL DEFAULT FALSE,
    is_entry_point       BOOLEAN NOT NULL DEFAULT FALSE,
    pagerank             DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    betweenness          DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    community_id         INTEGER NOT NULL DEFAULT 0,
    community_meta_json  TEXT NOT NULL DEFAULT '{}',
    kind                 TEXT,
    name                 TEXT,
    qualified_name       TEXT,
    file_path            TEXT,
    start_line           INTEGER,
    end_line             INTEGER,
    visibility           TEXT,
    signature            TEXT,
    parent_symbol_id     TEXT,
    external_system_id   BIGINT REFERENCES external_systems(id) ON DELETE SET NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_graph_node ON graph_nodes(repository_id, node_id);
CREATE INDEX IF NOT EXISTS ix_graph_nodes_repo_type_community ON graph_nodes(repository_id, node_type, community_id);
CREATE INDEX IF NOT EXISTS ix_graph_nodes_external_system_id ON graph_nodes(external_system_id);

CREATE TABLE IF NOT EXISTS graph_edges (
    id                   TEXT PRIMARY KEY,
    repository_id        TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    source_node_id       TEXT NOT NULL,
    target_node_id       TEXT NOT NULL,
    imported_names_json  TEXT NOT NULL DEFAULT '[]',
    edge_type            TEXT NOT NULL DEFAULT 'imports',
    confidence           DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_graph_edge_typed ON graph_edges(repository_id, source_node_id, target_node_id, edge_type);
CREATE INDEX IF NOT EXISTS ix_graph_edges_repo_source_type ON graph_edges(repository_id, source_node_id, edge_type);
CREATE INDEX IF NOT EXISTS ix_graph_edges_repo_target_type ON graph_edges(repository_id, target_node_id, edge_type);

CREATE TABLE IF NOT EXISTS webhook_events (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT REFERENCES repositories(id) ON DELETE SET NULL,
    provider       TEXT NOT NULL,
    event_type     TEXT NOT NULL,
    delivery_id    TEXT NOT NULL DEFAULT '',
    payload_json   TEXT NOT NULL DEFAULT '{}',
    processed      BOOLEAN NOT NULL DEFAULT FALSE,
    job_id         TEXT REFERENCES generation_jobs(id) ON DELETE SET NULL,
    received_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wiki_symbols (
    id                   TEXT PRIMARY KEY,
    repository_id        TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    file_path            TEXT NOT NULL,
    symbol_id            TEXT NOT NULL,
    name                 TEXT NOT NULL,
    qualified_name       TEXT NOT NULL,
    kind                 TEXT NOT NULL,
    signature            TEXT NOT NULL DEFAULT '',
    start_line           INTEGER NOT NULL DEFAULT 0,
    end_line             INTEGER NOT NULL DEFAULT 0,
    docstring            TEXT,
    visibility           TEXT NOT NULL DEFAULT 'public',
    is_async             BOOLEAN NOT NULL DEFAULT FALSE,
    complexity_estimate  INTEGER NOT NULL DEFAULT 0,
    language             TEXT NOT NULL DEFAULT '',
    parent_name          TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_wiki_symbol ON wiki_symbols(repository_id, symbol_id);

CREATE TABLE IF NOT EXISTS git_metadata (
    id                          TEXT PRIMARY KEY,
    repository_id               TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    file_path                   TEXT NOT NULL,
    commit_count_total          INTEGER NOT NULL DEFAULT 0,
    commit_count_90d            INTEGER NOT NULL DEFAULT 0,
    commit_count_30d            INTEGER NOT NULL DEFAULT 0,
    commit_count_capped         BOOLEAN NOT NULL DEFAULT FALSE,
    first_commit_at             TIMESTAMPTZ,
    last_commit_at              TIMESTAMPTZ,
    primary_owner_name          TEXT,
    primary_owner_email         TEXT,
    primary_owner_commit_pct    DOUBLE PRECISION,
    recent_owner_name           TEXT,
    recent_owner_commit_pct     DOUBLE PRECISION,
    top_authors_json            TEXT NOT NULL DEFAULT '[]',
    significant_commits_json    TEXT NOT NULL DEFAULT '[]',
    co_change_partners_json     TEXT NOT NULL DEFAULT '[]',
    commit_categories_json      TEXT NOT NULL DEFAULT '{}',
    is_hotspot                  BOOLEAN NOT NULL DEFAULT FALSE,
    is_stable                   BOOLEAN NOT NULL DEFAULT FALSE,
    churn_percentile            DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    age_days                    INTEGER NOT NULL DEFAULT 0,
    lines_added_90d             INTEGER NOT NULL DEFAULT 0,
    lines_deleted_90d           INTEGER NOT NULL DEFAULT 0,
    avg_commit_size             DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    bus_factor                  INTEGER NOT NULL DEFAULT 0,
    contributor_count           INTEGER NOT NULL DEFAULT 0,
    original_path               TEXT,
    merge_commit_count_90d      INTEGER NOT NULL DEFAULT 0,
    temporal_hotspot_score      DOUBLE PRECISION DEFAULT 0.0,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_git_metadata ON git_metadata(repository_id, file_path);
CREATE INDEX IF NOT EXISTS ix_git_metadata_repository_id ON git_metadata(repository_id);
CREATE INDEX IF NOT EXISTS ix_git_metadata_repo_file ON git_metadata(repository_id, file_path);

CREATE TABLE IF NOT EXISTS dead_code_findings (
    id                TEXT PRIMARY KEY,
    repository_id     TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL,
    file_path         TEXT NOT NULL,
    symbol_name       TEXT,
    symbol_kind       TEXT,
    confidence        DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    reason            TEXT NOT NULL DEFAULT '',
    last_commit_at    TIMESTAMPTZ,
    commit_count_90d  INTEGER NOT NULL DEFAULT 0,
    lines             INTEGER NOT NULL DEFAULT 0,
    package           TEXT,
    evidence_json     TEXT NOT NULL DEFAULT '[]',
    safe_to_delete    BOOLEAN NOT NULL DEFAULT FALSE,
    primary_owner     TEXT,
    age_days          INTEGER,
    status            TEXT NOT NULL DEFAULT 'open',
    note              TEXT,
    analyzed_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_dead_code_findings_repository_id ON dead_code_findings(repository_id);

CREATE TABLE IF NOT EXISTS decision_records (
    id                       TEXT PRIMARY KEY,
    repository_id            TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    title                    TEXT NOT NULL,
    status                   TEXT NOT NULL DEFAULT 'proposed',
    context                  TEXT NOT NULL DEFAULT '',
    decision                 TEXT NOT NULL DEFAULT '',
    rationale                TEXT NOT NULL DEFAULT '',
    alternatives_json        TEXT NOT NULL DEFAULT '[]',
    consequences_json        TEXT NOT NULL DEFAULT '[]',
    affected_files_json      TEXT NOT NULL DEFAULT '[]',
    affected_modules_json    TEXT NOT NULL DEFAULT '[]',
    tags_json                TEXT NOT NULL DEFAULT '[]',
    evidence_commits_json    TEXT NOT NULL DEFAULT '[]',
    source                   TEXT NOT NULL DEFAULT 'cli',
    evidence_file            TEXT,
    evidence_line            INTEGER,
    confidence               DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    verification             TEXT NOT NULL DEFAULT 'unverified',
    last_code_change         TIMESTAMPTZ,
    staleness_score          DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    superseded_by            TEXT,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_decision_record ON decision_records(repository_id, title, source, evidence_file);
CREATE INDEX IF NOT EXISTS ix_decision_records_repository_id ON decision_records(repository_id);
CREATE INDEX IF NOT EXISTS ix_decision_records_status ON decision_records(repository_id, status);
CREATE INDEX IF NOT EXISTS ix_decision_records_source ON decision_records(repository_id, source);

CREATE TABLE IF NOT EXISTS decision_evidence (
    id              TEXT PRIMARY KEY,
    decision_id     TEXT NOT NULL REFERENCES decision_records(id) ON DELETE CASCADE,
    source          TEXT NOT NULL,
    source_rank     INTEGER NOT NULL DEFAULT 1,
    evidence_file   TEXT,
    evidence_line   INTEGER,
    evidence_commit TEXT,
    source_quote    TEXT NOT NULL DEFAULT '',
    confidence      DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    verification    TEXT NOT NULL DEFAULT 'unverified',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_decision_evidence ON decision_evidence(decision_id, source, evidence_file, evidence_commit);
CREATE INDEX IF NOT EXISTS ix_decision_evidence_decision_id ON decision_evidence(decision_id);

CREATE TABLE IF NOT EXISTS decision_edges (
    id                TEXT PRIMARY KEY,
    repository_id     TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    src_decision_id   TEXT NOT NULL REFERENCES decision_records(id) ON DELETE CASCADE,
    dst_decision_id   TEXT NOT NULL REFERENCES decision_records(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL,
    confidence        DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    evidence          TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_decision_edge ON decision_edges(src_decision_id, dst_decision_id, kind);
CREATE INDEX IF NOT EXISTS ix_decision_edges_repo ON decision_edges(repository_id);
CREATE INDEX IF NOT EXISTS ix_decision_edges_src ON decision_edges(src_decision_id);
CREATE INDEX IF NOT EXISTS ix_decision_edges_dst ON decision_edges(dst_decision_id);

CREATE TABLE IF NOT EXISTS decision_node_links (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    decision_id    TEXT NOT NULL REFERENCES decision_records(id) ON DELETE CASCADE,
    node_id        TEXT NOT NULL,
    link_type      TEXT NOT NULL DEFAULT 'file',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_decision_node_link ON decision_node_links(decision_id, node_id, link_type);
CREATE INDEX IF NOT EXISTS ix_decision_node_links_repo ON decision_node_links(repository_id);
CREATE INDEX IF NOT EXISTS ix_decision_node_links_decision ON decision_node_links(decision_id);
CREATE INDEX IF NOT EXISTS ix_decision_node_links_node ON decision_node_links(node_id);

CREATE TABLE IF NOT EXISTS conversations (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    title          TEXT NOT NULL DEFAULT 'New conversation',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_conversations_repo_updated ON conversations(repository_id, updated_at);

CREATE TABLE IF NOT EXISTS chat_messages (
    id               TEXT PRIMARY KEY,
    conversation_id  TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role             TEXT NOT NULL,
    content_json     TEXT NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_chat_messages_conv_created ON chat_messages(conversation_id, created_at);

CREATE TABLE IF NOT EXISTS llm_costs (
    id             BIGSERIAL PRIMARY KEY,
    repository_id  TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    ts             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    model          TEXT NOT NULL,
    operation      TEXT NOT NULL,
    input_tokens   INTEGER NOT NULL,
    output_tokens  INTEGER NOT NULL,
    cost_usd       DOUBLE PRECISION NOT NULL,
    file_path      TEXT
);
CREATE INDEX IF NOT EXISTS ix_llm_costs_repository_ts ON llm_costs(repository_id, ts);

CREATE TABLE IF NOT EXISTS security_findings (
    id             BIGSERIAL PRIMARY KEY,
    repository_id  TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    file_path      TEXT NOT NULL,
    kind           TEXT NOT NULL,
    severity       TEXT NOT NULL,
    snippet        TEXT,
    line_number    INTEGER,
    detected_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_security_findings_repo_file ON security_findings(repository_id, file_path);

CREATE TABLE IF NOT EXISTS health_findings (
    id              TEXT PRIMARY KEY,
    repository_id   TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    file_path       TEXT NOT NULL,
    biomarker_type  TEXT NOT NULL,
    severity        TEXT NOT NULL,
    function_name   TEXT,
    line_start      INTEGER,
    line_end        INTEGER,
    details_json    TEXT NOT NULL DEFAULT '{}',
    health_impact   DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    reason          TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'open',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_health_findings_repository_id ON health_findings(repository_id);
CREATE INDEX IF NOT EXISTS ix_health_findings_repo_file ON health_findings(repository_id, file_path);

CREATE TABLE IF NOT EXISTS health_file_metrics (
    id                   TEXT PRIMARY KEY,
    repository_id        TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    file_path            TEXT NOT NULL,
    score                DOUBLE PRECISION NOT NULL DEFAULT 10.0,
    max_ccn              INTEGER NOT NULL DEFAULT 0,
    max_nesting          INTEGER NOT NULL DEFAULT 0,
    nloc                 INTEGER NOT NULL DEFAULT 0,
    duplication_pct      DOUBLE PRECISION,
    has_test_file        BOOLEAN NOT NULL DEFAULT FALSE,
    line_coverage_pct    DOUBLE PRECISION,
    branch_coverage_pct  DOUBLE PRECISION,
    module               TEXT,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_health_file_metrics ON health_file_metrics(repository_id, file_path);
CREATE INDEX IF NOT EXISTS ix_health_file_metrics_repo ON health_file_metrics(repository_id);

CREATE TABLE IF NOT EXISTS health_snapshots (
    id                     TEXT PRIMARY KEY,
    repository_id          TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    taken_at               TIMESTAMPTZ NOT NULL,
    hotspot_health         DOUBLE PRECISION NOT NULL DEFAULT 10.0,
    average_health         DOUBLE PRECISION NOT NULL DEFAULT 10.0,
    worst_performer_path   TEXT,
    worst_performer_score  DOUBLE PRECISION,
    per_file_scores_json   TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS ix_health_snapshots_repo ON health_snapshots(repository_id);

CREATE TABLE IF NOT EXISTS coverage_files (
    id                     TEXT PRIMARY KEY,
    repository_id          TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    file_path              TEXT NOT NULL,
    source_format          TEXT NOT NULL,
    line_coverage_pct      DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    branch_coverage_pct    DOUBLE PRECISION,
    covered_lines_json     TEXT NOT NULL DEFAULT '[]',
    total_coverable_lines  INTEGER NOT NULL DEFAULT 0,
    ingested_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ingested_commit_sha    TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_coverage_files ON coverage_files(repository_id, file_path);
CREATE INDEX IF NOT EXISTS ix_coverage_files_repo ON coverage_files(repository_id);

CREATE TABLE IF NOT EXISTS answer_cache (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    question_hash  TEXT NOT NULL,
    question       TEXT NOT NULL,
    payload_json   TEXT NOT NULL,
    provider_name  TEXT NOT NULL DEFAULT '',
    model_name     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_answer_cache_q ON answer_cache(repository_id, question_hash);
CREATE INDEX IF NOT EXISTS ix_answer_cache_repo ON answer_cache(repository_id);

CREATE TABLE IF NOT EXISTS graph_metrics (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    node_id        TEXT NOT NULL,
    pagerank       DOUBLE PRECISION NOT NULL DEFAULT 0,
    betweenness    DOUBLE PRECISION NOT NULL DEFAULT 0,
    community_id   INTEGER NOT NULL DEFAULT 0,
    in_degree      INTEGER NOT NULL DEFAULT 0,
    out_degree     INTEGER NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_graph_metric ON graph_metrics(repository_id, node_id);
CREATE INDEX IF NOT EXISTS ix_graph_metrics_repository_id ON graph_metrics(repository_id);

CREATE TABLE IF NOT EXISTS knowledge_graph_layers (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    layer_id       TEXT NOT NULL,
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    node_ids_json  TEXT NOT NULL DEFAULT '[]',
    display_order  INTEGER NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_kg_layer ON knowledge_graph_layers(repository_id, layer_id);
CREATE INDEX IF NOT EXISTS ix_kg_layers_repository_id ON knowledge_graph_layers(repository_id);

CREATE TABLE IF NOT EXISTS knowledge_graph_tour_steps (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    step_order     INTEGER NOT NULL,
    title          TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    node_ids_json  TEXT NOT NULL DEFAULT '[]',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_kg_tour_step ON knowledge_graph_tour_steps(repository_id, step_order);
CREATE INDEX IF NOT EXISTS ix_kg_tour_steps_repository_id ON knowledge_graph_tour_steps(repository_id);

CREATE TABLE IF NOT EXISTS pipeline_jobs (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    phase          TEXT NOT NULL,
    state          TEXT NOT NULL DEFAULT 'pending',
    cursor         TEXT,
    started_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    error          TEXT,
    metadata_json  TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS ix_pipeline_jobs_repository_id ON pipeline_jobs(repository_id);
CREATE INDEX IF NOT EXISTS ix_pipeline_jobs_repo_state ON pipeline_jobs(repository_id, state);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS pipeline_jobs;
DROP TABLE IF EXISTS knowledge_graph_tour_steps;
DROP TABLE IF EXISTS knowledge_graph_layers;
DROP TABLE IF EXISTS graph_metrics;
DROP TABLE IF EXISTS answer_cache;
DROP TABLE IF EXISTS coverage_files;
DROP TABLE IF EXISTS health_snapshots;
DROP TABLE IF EXISTS health_file_metrics;
DROP TABLE IF EXISTS health_findings;
DROP TABLE IF EXISTS security_findings;
DROP TABLE IF EXISTS llm_costs;
DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS conversations;
DROP TABLE IF EXISTS decision_node_links;
DROP TABLE IF EXISTS decision_edges;
DROP TABLE IF EXISTS decision_evidence;
DROP TABLE IF EXISTS decision_records;
DROP TABLE IF EXISTS dead_code_findings;
DROP TABLE IF EXISTS git_metadata;
DROP TABLE IF EXISTS wiki_symbols;
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS graph_edges;
DROP TABLE IF EXISTS graph_nodes;
DROP TABLE IF EXISTS external_systems;
DROP TABLE IF EXISTS wiki_page_versions;
DROP TABLE IF EXISTS wiki_pages;
DROP TABLE IF EXISTS generation_jobs;
DROP TABLE IF EXISTS repositories;
-- +goose StatementEnd
