-- +goose Up
-- +goose StatementBegin

-- Consolidated end-state schema derived from Python vor alembic migrations
-- 0001–0024. The Go port does not replay history; one consolidated migration is
-- equivalent at first install.
--
-- See docs/PORTING_PLAN.md §4 for context.

CREATE TABLE IF NOT EXISTS repositories (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    url             TEXT NOT NULL DEFAULT '',
    local_path      TEXT NOT NULL,
    default_branch  TEXT NOT NULL DEFAULT 'main',
    head_commit     TEXT,
    settings_json   TEXT NOT NULL DEFAULT '{}',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS generation_jobs (
    id               TEXT PRIMARY KEY,
    repository_id    TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending',
    provider_name    TEXT NOT NULL DEFAULT '',
    model_name       TEXT NOT NULL DEFAULT '',
    total_pages      INTEGER NOT NULL DEFAULT 0,
    completed_pages  INTEGER NOT NULL DEFAULT 0,
    failed_pages     INTEGER NOT NULL DEFAULT 0,
    current_level    INTEGER NOT NULL DEFAULT 0,
    error_message    TEXT,
    config_json      TEXT NOT NULL DEFAULT '{}',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at       DATETIME,
    finished_at      DATETIME,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS wiki_pages (
    id                TEXT PRIMARY KEY,
    repository_id     TEXT NOT NULL,
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
    confidence        REAL NOT NULL DEFAULT 1.0,
    freshness_status  TEXT NOT NULL DEFAULT 'fresh',
    metadata_json     TEXT NOT NULL DEFAULT '{}',
    human_notes       TEXT,
    created_at        DATETIME NOT NULL,
    updated_at        DATETIME NOT NULL,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);

-- SQLite FTS5 virtual table for wiki page full-text search. Maintained
-- application-side on wiki_pages insert/update/delete.
CREATE VIRTUAL TABLE IF NOT EXISTS page_fts USING fts5(
    page_id UNINDEXED,
    title,
    content
);

CREATE TABLE IF NOT EXISTS wiki_page_versions (
    id             TEXT PRIMARY KEY,
    page_id        TEXT NOT NULL,
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
    confidence     REAL NOT NULL DEFAULT 1.0,
    archived_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (page_id) REFERENCES wiki_pages(id)
);

CREATE TABLE IF NOT EXISTS graph_nodes (
    id                   TEXT PRIMARY KEY,
    repository_id        TEXT NOT NULL,
    node_id              TEXT NOT NULL,
    node_type            TEXT NOT NULL DEFAULT 'file',
    language             TEXT NOT NULL DEFAULT '',
    symbol_count         INTEGER NOT NULL DEFAULT 0,
    has_error            INTEGER NOT NULL DEFAULT 0,
    is_test              INTEGER NOT NULL DEFAULT 0,
    is_entry_point       INTEGER NOT NULL DEFAULT 0,
    pagerank             REAL NOT NULL DEFAULT 0.0,
    betweenness          REAL NOT NULL DEFAULT 0.0,
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
    external_system_id   INTEGER,
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE,
    FOREIGN KEY (external_system_id) REFERENCES external_systems(id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_graph_node ON graph_nodes(repository_id, node_id);
CREATE INDEX IF NOT EXISTS ix_graph_nodes_repo_type_community ON graph_nodes(repository_id, node_type, community_id);
CREATE INDEX IF NOT EXISTS ix_graph_nodes_external_system_id ON graph_nodes(external_system_id);

CREATE TABLE IF NOT EXISTS graph_edges (
    id                   TEXT PRIMARY KEY,
    repository_id        TEXT NOT NULL,
    source_node_id       TEXT NOT NULL,
    target_node_id       TEXT NOT NULL,
    imported_names_json  TEXT NOT NULL DEFAULT '[]',
    edge_type            TEXT NOT NULL DEFAULT 'imports',
    confidence           REAL NOT NULL DEFAULT 1.0,
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_graph_edge_typed ON graph_edges(repository_id, source_node_id, target_node_id, edge_type);
CREATE INDEX IF NOT EXISTS ix_graph_edges_repo_source_type ON graph_edges(repository_id, source_node_id, edge_type);
CREATE INDEX IF NOT EXISTS ix_graph_edges_repo_target_type ON graph_edges(repository_id, target_node_id, edge_type);

CREATE TABLE IF NOT EXISTS webhook_events (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT,
    provider       TEXT NOT NULL,
    event_type     TEXT NOT NULL,
    delivery_id    TEXT NOT NULL DEFAULT '',
    payload_json   TEXT NOT NULL DEFAULT '{}',
    processed      INTEGER NOT NULL DEFAULT 0,
    job_id         TEXT,
    received_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE SET NULL,
    FOREIGN KEY (job_id) REFERENCES generation_jobs(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS wiki_symbols (
    id                   TEXT PRIMARY KEY,
    repository_id        TEXT NOT NULL,
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
    is_async             INTEGER NOT NULL DEFAULT 0,
    complexity_estimate  INTEGER NOT NULL DEFAULT 0,
    language             TEXT NOT NULL DEFAULT '',
    parent_name          TEXT,
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_wiki_symbol ON wiki_symbols(repository_id, symbol_id);

CREATE TABLE IF NOT EXISTS git_metadata (
    id                          TEXT PRIMARY KEY,
    repository_id               TEXT NOT NULL,
    file_path                   TEXT NOT NULL,
    commit_count_total          INTEGER NOT NULL DEFAULT 0,
    commit_count_90d            INTEGER NOT NULL DEFAULT 0,
    commit_count_30d            INTEGER NOT NULL DEFAULT 0,
    commit_count_capped         INTEGER NOT NULL DEFAULT 0,
    first_commit_at             DATETIME,
    last_commit_at              DATETIME,
    primary_owner_name          TEXT,
    primary_owner_email         TEXT,
    primary_owner_commit_pct    REAL,
    recent_owner_name           TEXT,
    recent_owner_commit_pct     REAL,
    top_authors_json            TEXT NOT NULL DEFAULT '[]',
    significant_commits_json    TEXT NOT NULL DEFAULT '[]',
    co_change_partners_json     TEXT NOT NULL DEFAULT '[]',
    commit_categories_json      TEXT NOT NULL DEFAULT '{}',
    is_hotspot                  INTEGER NOT NULL DEFAULT 0,
    is_stable                   INTEGER NOT NULL DEFAULT 0,
    churn_percentile            REAL NOT NULL DEFAULT 0.0,
    age_days                    INTEGER NOT NULL DEFAULT 0,
    lines_added_90d             INTEGER NOT NULL DEFAULT 0,
    lines_deleted_90d           INTEGER NOT NULL DEFAULT 0,
    avg_commit_size             REAL NOT NULL DEFAULT 0.0,
    bus_factor                  INTEGER NOT NULL DEFAULT 0,
    contributor_count           INTEGER NOT NULL DEFAULT 0,
    original_path               TEXT,
    merge_commit_count_90d      INTEGER NOT NULL DEFAULT 0,
    temporal_hotspot_score      REAL DEFAULT 0.0,
    created_at                  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at                  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_git_metadata ON git_metadata(repository_id, file_path);
CREATE INDEX IF NOT EXISTS ix_git_metadata_repository_id ON git_metadata(repository_id);
CREATE INDEX IF NOT EXISTS ix_git_metadata_repo_file ON git_metadata(repository_id, file_path);

CREATE TABLE IF NOT EXISTS dead_code_findings (
    id              TEXT PRIMARY KEY,
    repository_id   TEXT NOT NULL,
    kind            TEXT NOT NULL,
    file_path       TEXT NOT NULL,
    symbol_name     TEXT,
    symbol_kind     TEXT,
    confidence      REAL NOT NULL DEFAULT 0.0,
    reason          TEXT NOT NULL DEFAULT '',
    last_commit_at  DATETIME,
    commit_count_90d INTEGER NOT NULL DEFAULT 0,
    lines           INTEGER NOT NULL DEFAULT 0,
    package         TEXT,
    evidence_json   TEXT NOT NULL DEFAULT '[]',
    safe_to_delete  INTEGER NOT NULL DEFAULT 0,
    primary_owner   TEXT,
    age_days        INTEGER,
    status          TEXT NOT NULL DEFAULT 'open',
    note            TEXT,
    analyzed_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS ix_dead_code_findings_repository_id ON dead_code_findings(repository_id);

CREATE TABLE IF NOT EXISTS decision_records (
    id                       TEXT PRIMARY KEY,
    repository_id            TEXT NOT NULL,
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
    confidence               REAL NOT NULL DEFAULT 1.0,
    verification             TEXT NOT NULL DEFAULT 'unverified',
    last_code_change         DATETIME,
    staleness_score          REAL NOT NULL DEFAULT 0.0,
    superseded_by            TEXT,
    created_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_decision_record ON decision_records(repository_id, title, source, evidence_file);
CREATE INDEX IF NOT EXISTS ix_decision_records_repository_id ON decision_records(repository_id);
CREATE INDEX IF NOT EXISTS ix_decision_records_status ON decision_records(repository_id, status);
CREATE INDEX IF NOT EXISTS ix_decision_records_source ON decision_records(repository_id, source);

CREATE TABLE IF NOT EXISTS decision_evidence (
    id              TEXT PRIMARY KEY,
    decision_id     TEXT NOT NULL,
    source          TEXT NOT NULL,
    source_rank     INTEGER NOT NULL DEFAULT 1,
    evidence_file   TEXT,
    evidence_line   INTEGER,
    evidence_commit TEXT,
    source_quote    TEXT NOT NULL DEFAULT '',
    confidence      REAL NOT NULL DEFAULT 0.0,
    verification    TEXT NOT NULL DEFAULT 'unverified',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (decision_id) REFERENCES decision_records(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_decision_evidence ON decision_evidence(decision_id, source, evidence_file, evidence_commit);
CREATE INDEX IF NOT EXISTS ix_decision_evidence_decision_id ON decision_evidence(decision_id);

CREATE TABLE IF NOT EXISTS decision_edges (
    id                TEXT PRIMARY KEY,
    repository_id     TEXT NOT NULL,
    src_decision_id   TEXT NOT NULL,
    dst_decision_id   TEXT NOT NULL,
    kind              TEXT NOT NULL,
    confidence        REAL NOT NULL DEFAULT 0.5,
    evidence          TEXT NOT NULL DEFAULT '',
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE,
    FOREIGN KEY (src_decision_id) REFERENCES decision_records(id) ON DELETE CASCADE,
    FOREIGN KEY (dst_decision_id) REFERENCES decision_records(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_decision_edge ON decision_edges(src_decision_id, dst_decision_id, kind);
CREATE INDEX IF NOT EXISTS ix_decision_edges_repo ON decision_edges(repository_id);
CREATE INDEX IF NOT EXISTS ix_decision_edges_src ON decision_edges(src_decision_id);
CREATE INDEX IF NOT EXISTS ix_decision_edges_dst ON decision_edges(dst_decision_id);

CREATE TABLE IF NOT EXISTS decision_node_links (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL,
    decision_id    TEXT NOT NULL,
    node_id        TEXT NOT NULL,
    link_type      TEXT NOT NULL DEFAULT 'file',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE,
    FOREIGN KEY (decision_id) REFERENCES decision_records(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_decision_node_link ON decision_node_links(decision_id, node_id, link_type);
CREATE INDEX IF NOT EXISTS ix_decision_node_links_repo ON decision_node_links(repository_id);
CREATE INDEX IF NOT EXISTS ix_decision_node_links_decision ON decision_node_links(decision_id);
CREATE INDEX IF NOT EXISTS ix_decision_node_links_node ON decision_node_links(node_id);

CREATE TABLE IF NOT EXISTS conversations (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL,
    title          TEXT NOT NULL DEFAULT 'New conversation',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS ix_conversations_repo_updated ON conversations(repository_id, updated_at);

CREATE TABLE IF NOT EXISTS chat_messages (
    id               TEXT PRIMARY KEY,
    conversation_id  TEXT NOT NULL,
    role             TEXT NOT NULL,
    content_json     TEXT NOT NULL DEFAULT '{}',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS ix_chat_messages_conv_created ON chat_messages(conversation_id, created_at);

CREATE TABLE IF NOT EXISTS llm_costs (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    repository_id  TEXT NOT NULL,
    ts             DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    model          TEXT NOT NULL,
    operation      TEXT NOT NULL,
    input_tokens   INTEGER NOT NULL,
    output_tokens  INTEGER NOT NULL,
    cost_usd       REAL NOT NULL,
    file_path      TEXT,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS ix_llm_costs_repository_ts ON llm_costs(repository_id, ts);

-- security_findings: Go port adds the missing FK that the Python schema omits
-- (see schema research note). Behaviour is otherwise identical.
CREATE TABLE IF NOT EXISTS security_findings (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    repository_id  TEXT NOT NULL,
    file_path      TEXT NOT NULL,
    kind           TEXT NOT NULL,
    severity       TEXT NOT NULL,
    snippet        TEXT,
    line_number    INTEGER,
    detected_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS ix_security_findings_repo_file ON security_findings(repository_id, file_path);

CREATE TABLE IF NOT EXISTS external_systems (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    repository_id  TEXT NOT NULL,
    name           TEXT NOT NULL,
    display_name   TEXT NOT NULL DEFAULT '',
    ecosystem      TEXT NOT NULL,
    category       TEXT NOT NULL DEFAULT 'library',
    version        TEXT,
    declared_in    TEXT NOT NULL,
    is_dev_dep     INTEGER NOT NULL DEFAULT 0,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_external_system ON external_systems(repository_id, name, declared_in);
CREATE INDEX IF NOT EXISTS ix_external_systems_repository_id ON external_systems(repository_id);

CREATE TABLE IF NOT EXISTS health_findings (
    id              TEXT PRIMARY KEY,
    repository_id   TEXT NOT NULL,
    file_path       TEXT NOT NULL,
    biomarker_type  TEXT NOT NULL,
    severity        TEXT NOT NULL,
    function_name   TEXT,
    line_start      INTEGER,
    line_end        INTEGER,
    details_json    TEXT NOT NULL DEFAULT '{}',
    health_impact   REAL NOT NULL DEFAULT 0.0,
    reason          TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'open',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS ix_health_findings_repository_id ON health_findings(repository_id);
CREATE INDEX IF NOT EXISTS ix_health_findings_repo_file ON health_findings(repository_id, file_path);

CREATE TABLE IF NOT EXISTS health_file_metrics (
    id                   TEXT PRIMARY KEY,
    repository_id        TEXT NOT NULL,
    file_path            TEXT NOT NULL,
    score                REAL NOT NULL DEFAULT 10.0,
    max_ccn              INTEGER NOT NULL DEFAULT 0,
    max_nesting          INTEGER NOT NULL DEFAULT 0,
    nloc                 INTEGER NOT NULL DEFAULT 0,
    duplication_pct      REAL,
    has_test_file        INTEGER NOT NULL DEFAULT 0,
    line_coverage_pct    REAL,
    branch_coverage_pct  REAL,
    module               TEXT,
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_health_file_metrics ON health_file_metrics(repository_id, file_path);
CREATE INDEX IF NOT EXISTS ix_health_file_metrics_repo ON health_file_metrics(repository_id);

CREATE TABLE IF NOT EXISTS health_snapshots (
    id                     TEXT PRIMARY KEY,
    repository_id          TEXT NOT NULL,
    taken_at               DATETIME NOT NULL,
    hotspot_health         REAL NOT NULL DEFAULT 10.0,
    average_health         REAL NOT NULL DEFAULT 10.0,
    worst_performer_path   TEXT,
    worst_performer_score  REAL,
    per_file_scores_json   TEXT NOT NULL DEFAULT '{}',
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS ix_health_snapshots_repo ON health_snapshots(repository_id);

CREATE TABLE IF NOT EXISTS coverage_files (
    id                     TEXT PRIMARY KEY,
    repository_id          TEXT NOT NULL,
    file_path              TEXT NOT NULL,
    source_format          TEXT NOT NULL,
    line_coverage_pct      REAL NOT NULL DEFAULT 0.0,
    branch_coverage_pct    REAL,
    covered_lines_json     TEXT NOT NULL DEFAULT '[]',
    total_coverable_lines  INTEGER NOT NULL DEFAULT 0,
    ingested_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ingested_commit_sha    TEXT,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_coverage_files ON coverage_files(repository_id, file_path);
CREATE INDEX IF NOT EXISTS ix_coverage_files_repo ON coverage_files(repository_id);

CREATE TABLE IF NOT EXISTS answer_cache (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL,
    question_hash  TEXT NOT NULL,
    question       TEXT NOT NULL,
    payload_json   TEXT NOT NULL,
    provider_name  TEXT NOT NULL DEFAULT '',
    model_name     TEXT NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_answer_cache_q ON answer_cache(repository_id, question_hash);
CREATE INDEX IF NOT EXISTS ix_answer_cache_repo ON answer_cache(repository_id);

CREATE TABLE IF NOT EXISTS graph_metrics (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL,
    node_id        TEXT NOT NULL,
    pagerank       REAL NOT NULL DEFAULT 0,
    betweenness    REAL NOT NULL DEFAULT 0,
    community_id   INTEGER NOT NULL DEFAULT 0,
    in_degree      INTEGER NOT NULL DEFAULT 0,
    out_degree     INTEGER NOT NULL DEFAULT 0,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_graph_metric ON graph_metrics(repository_id, node_id);
CREATE INDEX IF NOT EXISTS ix_graph_metrics_repository_id ON graph_metrics(repository_id);

CREATE TABLE IF NOT EXISTS knowledge_graph_layers (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL,
    layer_id       TEXT NOT NULL,
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    node_ids_json  TEXT NOT NULL DEFAULT '[]',
    display_order  INTEGER NOT NULL DEFAULT 0,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_kg_layer ON knowledge_graph_layers(repository_id, layer_id);
CREATE INDEX IF NOT EXISTS ix_kg_layers_repository_id ON knowledge_graph_layers(repository_id);

CREATE TABLE IF NOT EXISTS knowledge_graph_tour_steps (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL,
    step_order     INTEGER NOT NULL,
    title          TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    node_ids_json  TEXT NOT NULL DEFAULT '[]',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_kg_tour_step ON knowledge_graph_tour_steps(repository_id, step_order);
CREATE INDEX IF NOT EXISTS ix_kg_tour_steps_repository_id ON knowledge_graph_tour_steps(repository_id);

CREATE TABLE IF NOT EXISTS pipeline_jobs (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL,
    phase          TEXT NOT NULL,
    state          TEXT NOT NULL DEFAULT 'pending',
    cursor         TEXT,
    started_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    error          TEXT,
    metadata_json  TEXT NOT NULL DEFAULT '{}',
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
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
DROP TABLE IF EXISTS external_systems;
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
DROP TABLE IF EXISTS wiki_page_versions;
DROP TABLE IF EXISTS page_fts;
DROP TABLE IF EXISTS wiki_pages;
DROP TABLE IF EXISTS generation_jobs;
DROP TABLE IF EXISTS repositories;
-- +goose StatementEnd
