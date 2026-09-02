# `runResearch` — company research pipeline

Source: `cmd/dolores/main.go:203` (`runResearch`) and `cmd/dolores/main.go:255` (`runCompany`).

Runs per-symbol (`-symbol` required; exits early if empty). Wires up a 90s HTTP client, Archivus storage (10-min timeout), Mongo store, OpenRouter LLM client, and an LLM budget (max requests/tokens from config), then executes `runCompany` with timing + LLM-usage logging.

## 1. Discovery (`research.DiscoverCompany`, internal/research/discovery.go:64)

- DuckDuckGo-searches `"<name> <exchange> investor relations annual report pdf"`; picks the **official website** by scoring hosts against company-name tokens while filtering blocked hosts (wikipedia, LinkedIn, aggregators, etc.).
- Finds the **investor-relations link** by scanning homepage anchors for "investor"/"shareholder" (shallowest path wins), falling back to conventional paths (`/investor-relations`, `/investors`, `/ir`, ...).
- Harvests **PDF candidates** from the IR page, classified by regex into `annual_report` / `investor_presentation` with fiscal-year labels (e.g. `FY2025-26`).
- If no annual reports: chases "annual" links one level deep, searches NSE archives (`nsearchives.nseindia.com`), then targeted `site:` / `filetype:pdf` searches.
- Dedupes, ranks (kind, then latest FY first), and saves official site + IR link to the DB.

## 2. Collection (`Collector.Collect`, internal/research/collector.go:58)

Downloads and archives into `<SYMBOL>/research/` (skips already-archived files by name):

- Best (latest) **annual report PDF**
- Up to 2 latest **investor presentations**
- Snapshot of the **IR index page** (HTML/txt)
- For manufacturers: **product/service page text** scraped from up to 8 product pages (`/products`, etc.), joined into one txt

File metadata (title, kind, FY, source URL, Archivus path, signed URL) goes to `analysis_files`; in-memory bytes are returned for extraction.

## 3. Topic extraction (per segment: `products`, `raw_inputs`, `revenue_split`)

Upserts a pending `company_resources` row, then LLM-extracts structured JSON from the collected docs (`ExtractTopics`, internal/research/extractor.go:144):

- **products** → business summary + product categories (name, description, revenue-share hint)
- **raw_inputs** → manufacturing flag + raw materials (material, use, sourcing regions)
- **revenue_split** → FY, currency, per-segment revenue pct/value

Stored via `SetExtractedData`; scanned PDFs without a text layer stay pending. Budget exhaustion skips remaining segments.

## 4. Review (`reviewRowState`, main.go:410)

Prints all the company's `company_resources` rows: segment, state (pending vs extracted JSON), research link, and whether raw data is archived.
