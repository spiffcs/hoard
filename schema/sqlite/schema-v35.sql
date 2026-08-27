-- hoard.db schema, version 35.

CREATE TABLE card_bid_history (
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    finish TEXT NOT NULL CHECK (finish IN ('nonfoil', 'foil', 'etched')),
    price_usd REAL NOT NULL,
    source TEXT NOT NULL,
    as_of TEXT NOT NULL,
    PRIMARY KEY (scryfall_id, finish, as_of)
);

CREATE TABLE card_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    container_id INTEGER NOT NULL REFERENCES containers(id) ON DELETE CASCADE,
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id),
    finish TEXT NOT NULL CHECK (finish IN ('nonfoil', 'foil', 'etched')),
    condition TEXT NOT NULL DEFAULT 'unknown',
    board TEXT NOT NULL DEFAULT 'main',
    purchase_price REAL,
    quantity INTEGER NOT NULL
);

CREATE TABLE card_price_gaps (
    scryfall_id TEXT PRIMARY KEY REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    checked_at TEXT NOT NULL
);

CREATE TABLE card_price_history (
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    finish TEXT NOT NULL CHECK (finish IN ('nonfoil', 'foil', 'etched')),
    price_usd REAL NOT NULL,
    source TEXT NOT NULL,
    as_of TEXT NOT NULL,
    PRIMARY KEY (scryfall_id, finish, as_of)
);

CREATE TABLE card_price_overrides (
    scryfall_id TEXT PRIMARY KEY REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    price_usd REAL,
    price_usd_foil REAL,
    price_usd_etched REAL,
    refused_usd REAL,
    refused_usd_foil REAL,
    refused_usd_etched REAL,
    source TEXT NOT NULL,
    reason TEXT NOT NULL,
    as_of TEXT NOT NULL
);

CREATE TABLE card_prices_alt (
    scryfall_id TEXT PRIMARY KEY REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    mtgjson_uuid TEXT NOT NULL,
    price_usd REAL,
    price_usd_foil REAL,
    as_of TEXT NOT NULL,
    source_usd TEXT,
    source_usd_foil TEXT
);

CREATE TABLE cards (
    scryfall_id TEXT PRIMARY KEY,
    set_code TEXT NOT NULL,
    collector_number TEXT NOT NULL,
    name TEXT NOT NULL,
    price_usd REAL,
    price_usd_foil REAL,
    scryfall_url TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    mtgjson_uuid TEXT,
    raw_json TEXT,
    rarity TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.rarity')) VIRTUAL,
    set_name TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.set_name')) VIRTUAL,
    cmc REAL GENERATED ALWAYS AS (json_extract(raw_json,'$.cmc')) VIRTUAL,
    artist TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.artist')) VIRTUAL,
    released_at TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.released_at')) VIRTUAL,
    layout TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.layout')) VIRTUAL,
    type_line TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.type_line'), json_extract(raw_json,'$.card_faces[0].type_line'))) VIRTUAL,
    mana_cost TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.mana_cost'), json_extract(raw_json,'$.card_faces[0].mana_cost'))) VIRTUAL,
    oracle_text TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.oracle_text'), json_extract(raw_json,'$.card_faces[0].oracle_text'))) VIRTUAL,
    color_identity TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.color_identity')) VIRTUAL,
    power TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.power'), json_extract(raw_json,'$.card_faces[0].power'))) VIRTUAL,
    toughness TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.toughness'), json_extract(raw_json,'$.card_faces[0].toughness'))) VIRTUAL,
    loyalty TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.loyalty'), json_extract(raw_json,'$.card_faces[0].loyalty'))) VIRTUAL,
    flavor_text TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.flavor_text'), json_extract(raw_json,'$.card_faces[0].flavor_text'))) VIRTUAL,
    image_uri TEXT GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.image_uris.normal'), json_extract(raw_json,'$.card_faces[0].image_uris.normal'))) VIRTUAL,
    tcgplayer_id INTEGER GENERATED ALWAYS AS (json_extract(raw_json,'$.tcgplayer_id')) VIRTUAL,
    ck_url TEXT,
    ck_foil_url TEXT,
    promo_types TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.promo_types')) VIRTUAL,
    tcg_alt_product_id TEXT,
    ck_foil_id TEXT,
    ck_etched_id TEXT,
    tcg_product_id TEXT,
    price_usd_etched REAL,
    tcg_etched_product_id TEXT,
    lang TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.lang')) VIRTUAL,
    printed_name TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.printed_name')) VIRTUAL
);

CREATE TABLE containers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'manual',
    source_id TEXT,
    source_url TEXT,
    format TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    counted INTEGER NOT NULL DEFAULT 1,
    parent_id INTEGER REFERENCES containers(id) ON DELETE SET NULL,
    name_locked INTEGER NOT NULL DEFAULT 0,
    UNIQUE(source, source_id)
);

CREATE TABLE finish_guesses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    container_id INTEGER NOT NULL REFERENCES containers(id) ON DELETE CASCADE,
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id),
    finish TEXT NOT NULL,
    guessed_at TEXT NOT NULL
);

CREATE TABLE import_ledger (
    hash TEXT PRIMARY KEY,
    file TEXT NOT NULL,
    cards INTEGER NOT NULL,
    imported_at TEXT NOT NULL
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE value_snapshots (
    as_of TEXT PRIMARY KEY,
    binder REAL NOT NULL,
    decks REAL NOT NULL,
    total REAL NOT NULL,
    source TEXT NOT NULL DEFAULT 'observed'
);

CREATE TABLE watches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id),
    display TEXT NOT NULL,
    finish TEXT NOT NULL CHECK (finish IN ('nonfoil', 'foil', 'etched')),
    op TEXT NOT NULL,
    threshold REAL NOT NULL,
    created_at TEXT NOT NULL,
    last_state TEXT NOT NULL DEFAULT '',
    pct REAL NOT NULL DEFAULT 0,
    min_move REAL NOT NULL DEFAULT 0,
    window_days INTEGER NOT NULL DEFAULT 30,
    last_fired_at TEXT NOT NULL DEFAULT '',
    UNIQUE(scryfall_id, finish, op)
);

CREATE INDEX card_entries_card_id ON card_entries(scryfall_id);

CREATE UNIQUE INDEX card_entries_holding ON card_entries (
    container_id,
    scryfall_id,
    finish,
    condition,
    board,
    COALESCE(purchase_price, -1)
);

CREATE INDEX cards_mtgjson_uuid ON cards(mtgjson_uuid);

CREATE INDEX cards_name ON cards(name);

CREATE INDEX cards_set_code ON cards(set_code);

CREATE INDEX cards_trait_filter ON cards (
    type_line,
    artist,
    layout,
    set_name,
    rarity,
    cmc,
    color_identity,
    scryfall_id
);

CREATE INDEX containers_parent ON containers(parent_id);

CREATE INDEX finish_guesses_card ON finish_guesses (
    scryfall_id,
    finish
);

CREATE TRIGGER card_entries_folder_insert
BEFORE INSERT ON card_entries
WHEN (SELECT kind FROM containers WHERE id = NEW.container_id) = 'folder'
BEGIN
    SELECT RAISE(ABORT, 'a folder holds decks, not cards');
END;

CREATE TRIGGER card_entries_folder_update
BEFORE UPDATE OF container_id ON card_entries
WHEN (SELECT kind FROM containers WHERE id = NEW.container_id) = 'folder'
BEGIN
    SELECT RAISE(ABORT, 'a folder holds decks, not cards');
END;
