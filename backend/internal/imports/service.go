package imports

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/jobs"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/logger"
	"leadline/pkg/queue"
	"leadline/pkg/storage"
	"leadline/pkg/validate"
)

const MaxFileSize = 20 << 20

var ConsentBases = []string{"legitimate_interest", "consent", "contract", "public_business_contact"}

type Import struct {
	ID            uuid.UUID         `json:"id"`
	Filename      string            `json:"filename"`
	Status        string            `json:"status"`
	Mapping       map[string]string `json:"mapping"`
	Headers       []string          `json:"headers"`
	Source        string            `json:"source"`
	ConsentBasis  string            `json:"consent_basis"`
	TotalRows     int               `json:"total_rows"`
	ValidRows     int               `json:"valid_rows"`
	InvalidRows   int               `json:"invalid_rows"`
	DuplicateRows int               `json:"duplicate_rows"`
	ImportedRows  int               `json:"imported_rows"`
	Preview       json.RawMessage   `json:"preview"`
	Error         *string           `json:"error"`
	CreatedAt     time.Time         `json:"created_at"`
	CompletedAt   *time.Time        `json:"completed_at"`
}

type Preview struct {
	Rows     []Row `json:"rows"`     // first rows as they will be imported
	Problems []Row `json:"problems"` // first invalid / duplicate rows
}

type Service struct {
	pool  *pgxpool.Pool
	store storage.ObjectStore
	q     jobs.Enqueuer
	audit *audit.Log
}

func NewService(pool *pgxpool.Pool, store storage.ObjectStore, q jobs.Enqueuer, a *audit.Log) *Service {
	return &Service{pool: pool, store: store, q: q, audit: a}
}

const cols = `id, filename, status, mapping, headers, source, consent_basis, total_rows, valid_rows, invalid_rows,
	duplicate_rows, imported_rows, preview, error, created_at, completed_at`

func scan(row pgx.Row) (*Import, error) {
	var i Import
	var mapping, headers []byte
	err := row.Scan(&i.ID, &i.Filename, &i.Status, &mapping, &headers, &i.Source, &i.ConsentBasis, &i.TotalRows,
		&i.ValidRows, &i.InvalidRows, &i.DuplicateRows, &i.ImportedRows, &i.Preview, &i.Error, &i.CreatedAt, &i.CompletedAt)
	if database.IsNoRows(err) {
		return nil, apperr.NotFound("import")
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(mapping, &i.Mapping)
	_ = json.Unmarshal(headers, &i.Headers)
	return &i, nil
}

func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID) (*Import, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM imports WHERE organization_id=$1 AND id=$2`, orgID, id))
}

func (s *Service) List(ctx context.Context, p auth.Principal) ([]Import, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM imports WHERE organization_id=$1 ORDER BY created_at DESC LIMIT 50`, p.OrgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Import{}
	for rows.Next() {
		i, err := scan(rows)
		if err != nil {
			return nil, err
		}
		i.Preview = nil // keep the list light
		out = append(out, *i)
	}
	return out, rows.Err()
}

// Upload stores the file, parses its header, suggests a column mapping and
// builds the first preview.
func (s *Service) Upload(ctx context.Context, p auth.Principal, filename string, r io.Reader) (*Import, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileSize {
		return nil, apperr.Invalid("file is larger than 20 MB", nil)
	}
	headers, _, err := ReadCSV(bytes.NewReader(data))
	if err != nil {
		return nil, apperr.Invalid(err.Error(), nil)
	}
	id := uuid.New()
	key := fmt.Sprintf("imports/%s/%s.csv", p.OrgID, id)
	if err := s.store.Put(ctx, key, bytes.NewReader(data)); err != nil {
		return nil, err
	}
	mapping := SuggestMapping(headers)
	mj, _ := json.Marshal(mapping)
	hj, _ := json.Marshal(headers)
	name := filepath.Base(strings.TrimSpace(filename))
	if name == "" || name == "." {
		name = "upload.csv"
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO imports (id, organization_id, created_by, filename, storage_key, mapping, headers)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, p.OrgID, p.UserID, name, key, mj, hj); err != nil {
		return nil, err
	}
	if err := s.preview(ctx, p.OrgID, id); err != nil {
		return nil, err
	}
	return s.Get(ctx, p.OrgID, id)
}

type MappingInput struct {
	Mapping      map[string]string `json:"mapping"` // column index → field ("" to skip)
	Source       string            `json:"source"`
	ConsentBasis string            `json:"consent_basis"`
}

func (s *Service) UpdateMapping(ctx context.Context, p auth.Principal, id uuid.UUID, in MappingInput) (*Import, error) {
	imp, err := s.Get(ctx, p.OrgID, id)
	if err != nil {
		return nil, err
	}
	if imp.Status != "uploaded" && imp.Status != "previewed" {
		return nil, apperr.Conflict("import_locked", "import has already been started")
	}
	v := validate.Errors{}
	clean := map[string]string{}
	seen := map[string]bool{}
	for col, field := range in.Mapping {
		if field == "" {
			continue
		}
		if !contains(Fields, field) {
			v.Add("mapping", "unknown field "+field)
			continue
		}
		if seen[field] {
			v.Add("mapping", field+" is mapped more than once")
		}
		seen[field] = true
		clean[col] = field
	}
	v.Check(seen["company_name"], "mapping", "company_name must be mapped")
	if in.Source == "" {
		in.Source = imp.Source
	}
	if in.ConsentBasis == "" {
		in.ConsentBasis = imp.ConsentBasis
	}
	v.Check(validate.OneOf(in.ConsentBasis, ConsentBases...), "consent_basis", "must be one of "+strings.Join(ConsentBases, ", "))
	v.MaxLen("source", in.Source, 100)
	if err := v.Err(); err != nil {
		return nil, err
	}
	mj, _ := json.Marshal(clean)
	if _, err := s.pool.Exec(ctx, `UPDATE imports SET mapping=$3, source=$4, consent_basis=$5, updated_at=now() WHERE organization_id=$1 AND id=$2`,
		p.OrgID, id, mj, in.Source, in.ConsentBasis); err != nil {
		return nil, err
	}
	if err := s.preview(ctx, p.OrgID, id); err != nil {
		return nil, err
	}
	return s.Get(ctx, p.OrgID, id)
}

// Commit queues the import for background processing.
func (s *Service) Commit(ctx context.Context, p auth.Principal, id uuid.UUID) (*Import, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE imports SET status='queued', updated_at=now()
		WHERE organization_id=$1 AND id=$2 AND status='previewed' AND valid_rows - duplicate_rows > 0`, p.OrgID, id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		imp, err := s.Get(ctx, p.OrgID, id)
		if err != nil {
			return nil, err
		}
		if imp.Status == "previewed" {
			return nil, apperr.Conflict("nothing_to_import", "no new valid rows to import")
		}
		return nil, apperr.Conflict("import_locked", "import is not awaiting confirmation (status: "+imp.Status+")")
	}
	if err := s.q.Enqueue(ctx, jobs.QueueDefault, jobs.TypeProcessImport, jobs.ProcessImport{OrgID: p.OrgID, ImportID: id},
		queue.EnqueueOpts{MaxAttempts: 3}); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "import.started", EntityType: "import", EntityID: id.String()})
	return s.Get(ctx, p.OrgID, id)
}

// load parses the stored file with the import's current mapping and dedupes.
func (s *Service) load(ctx context.Context, orgID uuid.UUID, imp *Import) ([]Row, error) {
	var key string
	if err := s.pool.QueryRow(ctx, `SELECT storage_key FROM imports WHERE organization_id=$1 AND id=$2`, orgID, imp.ID).Scan(&key); err != nil {
		return nil, err
	}
	f, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	_, records, err := ReadCSV(f)
	if err != nil {
		return nil, apperr.Invalid(err.Error(), nil)
	}
	rows := BuildRows(records, imp.Mapping, imp.Source)
	existing, err := s.existingKeys(ctx, orgID, rows)
	if err != nil {
		return nil, err
	}
	Dedupe(rows, existing)
	return rows, nil
}

func (s *Service) preview(ctx context.Context, orgID, id uuid.UUID) error {
	imp, err := s.Get(ctx, orgID, id)
	if err != nil {
		return err
	}
	rows, err := s.load(ctx, orgID, imp)
	if err != nil {
		return err
	}
	var valid, invalid, dup int
	pv := Preview{Rows: []Row{}, Problems: []Row{}}
	for _, r := range rows {
		switch {
		case !r.Valid():
			invalid++
		case r.Duplicate != "":
			valid++
			dup++
		default:
			valid++
		}
		if r.Valid() && r.Duplicate == "" && len(pv.Rows) < 25 {
			pv.Rows = append(pv.Rows, r)
		}
		if (!r.Valid() || r.Duplicate != "") && len(pv.Problems) < 100 {
			pv.Problems = append(pv.Problems, r)
		}
	}
	pj, _ := json.Marshal(pv)
	_, err = s.pool.Exec(ctx, `
		UPDATE imports SET status='previewed', total_rows=$3, valid_rows=$4, invalid_rows=$5, duplicate_rows=$6,
			preview=$7, error=NULL, updated_at=now()
		WHERE organization_id=$1 AND id=$2`, orgID, id, len(rows), valid, invalid, dup, pj)
	return err
}

func (s *Service) existingKeys(ctx context.Context, orgID uuid.UUID, rows []Row) (ExistingKeys, error) {
	ex := ExistingKeys{Domains: map[string]bool{}, Emails: map[string]bool{}, Names: map[string]bool{}}
	domains, emails, names := Keys(rows)
	collect := func(dst map[string]bool, q string, args ...any) error {
		r, err := s.pool.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer r.Close()
		for r.Next() {
			var v string
			if err := r.Scan(&v); err != nil {
				return err
			}
			dst[strings.ToLower(v)] = true
		}
		return r.Err()
	}
	if len(domains) > 0 {
		if err := collect(ex.Domains, `SELECT domain FROM leads WHERE organization_id=$1 AND domain = ANY($2)`, orgID, domains); err != nil {
			return ex, err
		}
	}
	if len(emails) > 0 {
		if err := collect(ex.Emails, `
			SELECT email::text FROM lead_contacts WHERE organization_id=$1 AND email = ANY($2::citext[])
			UNION SELECT business_email::text FROM leads WHERE organization_id=$1 AND business_email = ANY($2::citext[])`, orgID, emails); err != nil {
			return ex, err
		}
	}
	if len(names) > 0 {
		var ns, cs []string
		for _, n := range names {
			name, city, _ := strings.Cut(n, "|")
			ns, cs = append(ns, name), append(cs, city)
		}
		if err := collect(ex.Names, `
			SELECT lower(company_name) || '|' || lower(city) FROM leads
			WHERE organization_id=$1 AND (lower(company_name), lower(city)) IN (SELECT * FROM unnest($2::text[], $3::text[]))`,
			orgID, ns, cs); err != nil {
			return ex, err
		}
	}
	return ex, nil
}

// Process runs the import (background job). Re-running after a partial
// failure is safe: already-imported rows are now duplicates and are skipped.
func (s *Service) Process(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE imports SET status='processing', updated_at=now()
		WHERE organization_id=$1 AND id=$2 AND status IN ('queued','processing')`, orgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // completed, failed or unknown: nothing to do
	}
	imp, err := s.Get(ctx, orgID, id)
	if err != nil {
		return err
	}
	rows, err := s.load(ctx, orgID, imp)
	if err != nil {
		return err
	}
	var todo []Row
	for _, r := range rows {
		if r.Valid() && r.Duplicate == "" {
			todo = append(todo, r)
		}
	}

	var leadIDs, contactIDs []uuid.UUID
	for start := 0; start < len(todo); start += 500 {
		chunk := todo[start:min(start+500, len(todo))]
		l, c, err := s.insertChunk(ctx, orgID, imp, chunk)
		if err != nil {
			return err
		}
		leadIDs, contactIDs = append(leadIDs, l...), append(contactIDs, c...)
		if _, err := s.pool.Exec(ctx, `UPDATE imports SET imported_rows = imported_rows + $3, updated_at=now() WHERE organization_id=$1 AND id=$2`,
			orgID, id, len(chunk)); err != nil {
			return err
		}
	}

	if _, err := s.pool.Exec(ctx, `
		UPDATE imports SET status='completed', completed_at=now(), updated_at=now(),
			imported_rows = (SELECT count(*) FROM leads WHERE import_id=$2)
		WHERE organization_id=$1 AND id=$2`, orgID, id); err != nil {
		return err
	}
	logger.From(ctx).Info("import completed", "import_id", id, "leads", len(leadIDs), "contacts", len(contactIDs))
	s.audit.Record(ctx, audit.Entry{OrgID: orgID, Action: "import.completed", EntityType: "import", EntityID: id.String(), Metadata: map[string]any{"leads": len(leadIDs)}})
	if err := jobs.EnqueueScore(ctx, s.q, orgID, leadIDs); err != nil {
		return err
	}
	return jobs.EnqueueVerify(ctx, s.q, orgID, contactIDs)
}

func (s *Service) insertChunk(ctx context.Context, orgID uuid.UUID, imp *Import, rows []Row) (leadIDs, contactIDs []uuid.UUID, err error) {
	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		b := &pgx.Batch{}
		for _, r := range rows {
			in := r.Lead
			leadID := uuid.New()
			leadIDs = append(leadIDs, leadID)
			social := []byte("[]")
			b.Queue(`
				INSERT INTO leads (id, organization_id, company_name, website, domain, industry, description, country, state,
					city, address, phone, business_email, linkedin_url, social_urls, employee_count, founded_year, source,
					source_url, import_id, status)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,'new')`,
				leadID, orgID, in.CompanyName, in.Website, r.Domain, in.Industry, in.Description, in.Country, in.State,
				in.City, in.Address, in.Phone, nullStr(in.BusinessEmail), in.LinkedInURL, social, in.EmployeeCount,
				in.FoundedYear, in.Source, in.SourceURL, imp.ID)
			b.Queue(`INSERT INTO lead_sources (organization_id, lead_id, source, source_url, import_id, consent_basis, metadata)
				VALUES ($1,$2,$3,$4,$5,$6,$7)`, orgID, leadID, in.Source, in.SourceURL, imp.ID, imp.ConsentBasis,
				map[string]any{"filename": imp.Filename, "row": r.Number})
			b.Queue(`INSERT INTO lead_events (organization_id, lead_id, event_type, data) VALUES ($1,$2,'imported',$3)`,
				orgID, leadID, map[string]any{"import_id": imp.ID, "filename": imp.Filename})
			if len(in.Tags) > 0 {
				b.Queue(`INSERT INTO lead_tags (organization_id, lead_id, tag) SELECT $1::uuid, $2::uuid, unnest($3::text[]) ON CONFLICT DO NOTHING`,
					orgID, leadID, in.Tags)
			}
			if c := r.Contact; c != nil {
				cid := uuid.New()
				if c.Email != "" {
					contactIDs = append(contactIDs, cid)
				}
				b.Queue(`INSERT INTO lead_contacts (id, organization_id, lead_id, first_name, last_name, job_title, email, source, is_primary)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8,true)`,
					cid, orgID, leadID, c.FirstName, c.LastName, c.JobTitle, nullStr(c.Email), c.Source)
			}
		}
		return tx.SendBatch(ctx, b).Close()
	})
	return leadIDs, contactIDs, err
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// HandleProcess is the worker entry point.
func (s *Service) HandleProcess(ctx context.Context, job queue.Job) error {
	var p jobs.ProcessImport
	if err := job.Decode(&p); err != nil {
		return queue.Permanent(err)
	}
	err := s.Process(ctx, p.OrgID, p.ImportID)
	if err != nil && (job.IsFinalAttempt() || queue.IsPermanent(err)) {
		_, _ = s.pool.Exec(context.WithoutCancel(ctx), `UPDATE imports SET status='failed', error=$3, updated_at=now() WHERE organization_id=$1 AND id=$2`,
			p.OrgID, p.ImportID, err.Error())
	}
	return err
}
