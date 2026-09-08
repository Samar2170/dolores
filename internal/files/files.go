// Package files provides an LLM-agent tool for browsing and retrieving the
// Archivus files archived for a company: market payload JSONs under
// <SYMBOL>/ and research material (annual reports, investor presentations,
// IR snapshots) under <SYMBOL>/research/.
package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"dolores/internal/models"
	"dolores/internal/storage"
	"dolores/internal/tool"
)

// ToolCompanyFiles is the tool name for company archive access.
const ToolCompanyFiles = "archivus_company_files"

// FilesArgs is the argument schema for archivus_company_files. Without file
// the tool lists the folder entries; with file it downloads that entry.
type FilesArgs struct {
	Symbol string `json:"symbol"`           // company symbol, e.g. "ITC" (required)
	Folder string `json:"folder,omitempty"` // subfolder below <SYMBOL>/, e.g. "research"
	File   string `json:"file,omitempty"`   // exact file name to download
	Out    string `json:"out,omitempty"`    // download destination path (default: the file name)
}

// listedFile is one entry of the JSON listing printed by the tool.
type listedFile struct {
	Name      string `json:"name"`
	IsDir     bool   `json:"is_dir,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	SignedURL string `json:"signed_url,omitempty"`
}

// CompanyFilesTool browses and fetches a company's Archivus files via
// tool.Tool. The symbol is validated against the companies collection, so
// only known companies are reachable and folder paths stay inside <SYMBOL>/.
type CompanyFilesTool struct {
	arch *storage.ArchivusClient
	db   *mongo.Database
}

// NewCompanyFilesTool creates the tool against the given Archivus client and
// database.
func NewCompanyFilesTool(arch *storage.ArchivusClient, db *mongo.Database) *CompanyFilesTool {
	return &CompanyFilesTool{arch: arch, db: db}
}

func (t *CompanyFilesTool) Name() string { return ToolCompanyFiles }

func (t *CompanyFilesTool) Description() string {
	return `Browse or retrieve the Archivus files archived for a company: market payload JSONs live under <SYMBOL>/ and research material (annual reports, investor presentations, IR snapshots) under <SYMBOL>/research. Without "file" the tool lists the folder entries with names, sizes and signed download URLs; with "file" it downloads that exact entry and writes it to "out" (default: the file name in the working directory). Args: {"symbol": "ITC", "folder": "research", "file": "ITC_annual_report_FY2026.pdf", "out": "/tmp/report.pdf"} - only symbol is required.`
}

func (t *CompanyFilesTool) Execute(ctx context.Context, args json.RawMessage) error {
	a, err := decodeArgs(args)
	if err != nil {
		return fmt.Errorf("%s: %w", ToolCompanyFiles, err)
	}

	co, err := t.company(ctx, a.Symbol)
	if err != nil {
		return fmt.Errorf("%s: %w", ToolCompanyFiles, err)
	}

	folder := co.Symbol
	if a.Folder != "" {
		folder = folder + "/" + a.Folder
	}

	entries, err := t.arch.List(folder)
	if err != nil {
		return fmt.Errorf("%s: list %s: %w", ToolCompanyFiles, folder, err)
	}

	if a.File == "" {
		return printListing(co, folder, entries)
	}
	return t.fetchFile(co, folder, entries, a)
}

func decodeArgs(args json.RawMessage) (FilesArgs, error) {
	var a FilesArgs
	if len(args) == 0 {
		return a, fmt.Errorf("symbol is required")
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return a, fmt.Errorf("decode args: %w", err)
	}
	if a.Symbol == "" {
		return a, fmt.Errorf("symbol is required")
	}
	a.Symbol = strings.TrimSpace(a.Symbol)
	a.Folder = strings.Trim(strings.TrimSpace(a.Folder), "/")
	if strings.Contains(a.Folder, "..") {
		return a, fmt.Errorf("folder must stay inside <SYMBOL>/")
	}
	a.File = strings.TrimSpace(a.File)
	if a.File != "" && (strings.ContainsRune(a.File, '/') || a.File == "." || a.File == "..") {
		return a, fmt.Errorf("file must be a plain name inside the folder")
	}
	return a, nil
}

// company resolves the symbol against the companies collection (a
// case-insensitive retry covers symbols typed in lower case).
func (t *CompanyFilesTool) company(ctx context.Context, symbol string) (*models.Company, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	col := t.db.Collection(models.ColCompanies)
	var co models.Company
	err := col.FindOne(ctx, bson.M{"symbol": symbol}).Decode(&co)
	if errors.Is(err, mongo.ErrNoDocuments) {
		err = col.FindOne(ctx, bson.M{"symbol": strings.ToUpper(symbol)}).Decode(&co)
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, fmt.Errorf("no company doc for symbol %q (run importnifty first?)", symbol)
	}
	if err != nil {
		return nil, err
	}
	return &co, nil
}

func printListing(co *models.Company, folder string, entries []storage.FileInfo) error {
	out := struct {
		Company string       `json:"company"`
		Folder  string       `json:"folder"`
		Files   []listedFile `json:"files"`
	}{Company: co.Name, Folder: folder, Files: make([]listedFile, 0, len(entries))}
	for _, e := range entries {
		f := listedFile{Name: e.Name, IsDir: e.IsDir, SignedURL: e.SignedURL}
		if !e.IsDir {
			f.SizeBytes = int64(e.Size)
		}
		out.Files = append(out.Files, f)
	}

	buf, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(buf))
	log.Printf("[company_files] %s: %d entries in %s", co.Symbol, len(entries), folder)
	return nil
}

func (t *CompanyFilesTool) fetchFile(co *models.Company, folder string, entries []storage.FileInfo, a FilesArgs) error {
	var target *storage.FileInfo
	for i := range entries {
		if !entries[i].IsDir && strings.EqualFold(entries[i].Name, a.File) {
			target = &entries[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("%s: no file %q in %s", ToolCompanyFiles, a.File, folder)
	}

	data, _, err := t.arch.Download(target.ID)
	if err != nil {
		return fmt.Errorf("%s: download %s: %w", ToolCompanyFiles, target.Name, err)
	}

	outPath := a.Out
	if outPath == "" {
		outPath = target.Name
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("%s: write %s: %w", ToolCompanyFiles, outPath, err)
	}

	out := struct {
		Company   string `json:"company"`
		File      string `json:"file"`
		Bytes     int    `json:"bytes"`
		SavedTo   string `json:"saved_to"`
		SignedURL string `json:"signed_url,omitempty"`
	}{Company: co.Name, File: target.Name, Bytes: len(data), SavedTo: outPath, SignedURL: target.SignedURL}
	buf, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(buf))
	log.Printf("[company_files] %s: downloaded %s (%d bytes) -> %s", co.Symbol, target.Name, len(data), outPath)
	return nil
}

// Compile-time interface check.
var _ tool.Tool = (*CompanyFilesTool)(nil)
