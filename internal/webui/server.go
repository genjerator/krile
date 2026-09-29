// Package webui serves a small, local-only web page that shows email-coverage
// statistics and a browsable list of companies from the PostgreSQL database.
package webui

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/output"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Stats is the email-coverage summary.
type Stats struct {
	Total        int
	WithEmail    int
	EmptyEmail   int
	PctWithEmail float64
	PctEmpty     float64
}

// Company is one row shown in the table.
type Company struct {
	ID                int64
	Name              string
	Category          string
	City              string
	Phone             string
	Website           string
	Email             string
	RelevantProducts  string
	Reasoning         string
	Subject           string
	PersonalizedEmail string
}

type pageData struct {
	Table      string
	Stats      Stats
	Rows       []Company
	Filter     string
	Campaign   string
	Query      string
	Category   string
	Categories []string
	Limit      int
	Shown      int
}

// Serve starts the local web server and blocks until the context is cancelled.
func Serve(ctx context.Context, cfg config.Config) error {
	table := cfg.DBTable
	if table == "" {
		table = "companies"
	}
	pool, err := pgxpool.New(ctx, output.PostgresConnString(cfg))
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	if err := ensureCampaignColumns(ctx, pool, table); err != nil {
		return err
	}

	srv := &server{pool: pool, table: table, tmpl: template.Must(template.New("page").Parse(pageHTML))}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)

	port := cfg.Port
	if port == 0 {
		port = 8080
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port) // local only
	httpSrv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	fmt.Printf("krile web UI on http://%s  (Ctrl+C to stop)\n", addr)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

type server struct {
	pool  *pgxpool.Pool
	table string
	tmpl  *template.Template
}

// emailsTable holds the per-company generated emails, one-to-many with the
// companies table via company_id.
const emailsTable = "company_emails"

// ensureCampaignColumns makes sure the company_emails child table exists, so
// --serve works even before a campaign run has populated it. Email content
// (subject/email_body + research) lives in this table, not on the
// companies row.
func ensureCampaignColumns(ctx context.Context, pool *pgxpool.Pool, table string) error {
	q := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		company_id         INTEGER NOT NULL REFERENCES %s(id) ON DELETE CASCADE,
		relevant_products  TEXT,
		reasoning          TEXT,
		subject            TEXT,
		email_body         TEXT,
		email_status       TEXT,
		email_sent_at      TIMESTAMPTZ,
		email_error        TEXT,
		created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
		pgx.Identifier{emailsTable}.Sanitize(), pgx.Identifier{table}.Sanitize())
	if _, err := pool.Exec(ctx, q); err != nil {
		return fmt.Errorf("create %s table: %w", emailsTable, err)
	}
	idx := fmt.Sprintf("CREATE INDEX IF NOT EXISTS company_emails_company_id_idx ON %s (company_id)",
		pgx.Identifier{emailsTable}.Sanitize())
	if _, err := pool.Exec(ctx, idx); err != nil {
		return fmt.Errorf("index %s: %w", emailsTable, err)
	}
	return nil
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	filter := r.URL.Query().Get("filter")
	if filter != "with" && filter != "empty" {
		filter = "all"
	}
	campaign := r.URL.Query().Get("campaign")
	if campaign != "filled" && campaign != "empty" {
		campaign = "all"
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	limit := 100
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 5000 {
		limit = l
	}

	stats, err := s.loadStats(ctx)
	if err != nil {
		http.Error(w, "stats query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	categories, err := s.loadCategories(ctx)
	if err != nil {
		http.Error(w, "categories query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rows, err := s.loadRows(ctx, filter, campaign, q, category, limit)
	if err != nil {
		http.Error(w, "list query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	data := pageData{
		Table:      s.table,
		Stats:      stats,
		Rows:       rows,
		Filter:     filter,
		Campaign:   campaign,
		Query:      q,
		Category:   category,
		Categories: categories,
		Limit:      limit,
		Shown:      len(rows),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *server) loadStats(ctx context.Context) (Stats, error) {
	q := fmt.Sprintf(`
		SELECT
		  count(*),
		  count(*) FILTER (WHERE email IS NOT NULL AND email <> ''),
		  count(*) FILTER (WHERE email IS NULL OR email = ''),
		  COALESCE(round(100.0 * count(*) FILTER (WHERE email IS NOT NULL AND email <> '') / NULLIF(count(*),0), 2), 0),
		  COALESCE(round(100.0 * count(*) FILTER (WHERE email IS NULL OR email = '') / NULLIF(count(*),0), 2), 0)
		FROM %s`, pgx.Identifier{s.table}.Sanitize())
	var st Stats
	err := s.pool.QueryRow(ctx, q).Scan(&st.Total, &st.WithEmail, &st.EmptyEmail, &st.PctWithEmail, &st.PctEmpty)
	return st, err
}

func (s *server) loadCategories(ctx context.Context) ([]string, error) {
	q := fmt.Sprintf(`SELECT DISTINCT category FROM %s WHERE category IS NOT NULL AND category <> '' ORDER BY category`,
		pgx.Identifier{s.table}.Sanitize())
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *server) loadRows(ctx context.Context, filter, campaign, search, category string, limit int) ([]Company, error) {
	var where []string
	var args []interface{}
	switch filter {
	case "with":
		where = append(where, "email IS NOT NULL AND email <> ''")
	case "empty":
		where = append(where, "(email IS NULL OR email = '')")
	}
	switch campaign {
	case "filled":
		where = append(where, "e.email_body IS NOT NULL AND e.email_body <> ''")
	case "empty":
		where = append(where, "(e.email_body IS NULL OR e.email_body = '')")
	}
	if search != "" {
		args = append(args, "%"+search+"%")
		where = append(where, fmt.Sprintf("(c.name ILIKE $%d OR c.city ILIKE $%d)", len(args), len(args)))
	}
	if category != "" {
		args = append(args, category)
		where = append(where, fmt.Sprintf("c.category = $%d", len(args)))
	}
	// Each company shows its most recent generated email (there can be many);
	// the LEFT JOIN LATERAL keeps companies with no email visible too.
	q := fmt.Sprintf(`SELECT c.id, c.name, COALESCE(c.category,''), COALESCE(c.city,''), COALESCE(c.phone,''), COALESCE(c.website,''), COALESCE(c.email,''),
		COALESCE(e.relevant_products,''), COALESCE(e.reasoning,''), COALESCE(e.subject,''), COALESCE(e.email_body,'')
		FROM %s c
		LEFT JOIN LATERAL (
			SELECT relevant_products, reasoning, subject, email_body
			FROM %s ce WHERE ce.company_id = c.id
			ORDER BY ce.created_at DESC LIMIT 1
		) e ON true`,
		pgx.Identifier{s.table}.Sanitize(), pgx.Identifier{emailsTable}.Sanitize())
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY c.id DESC LIMIT %d", limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Company
	for rows.Next() {
		var c Company
		if err := rows.Scan(&c.ID, &c.Name, &c.Category, &c.City, &c.Phone, &c.Website, &c.Email,
			&c.RelevantProducts, &c.Reasoning, &c.Subject, &c.PersonalizedEmail); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>krile — {{.Table}}</title>
<style>
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body { font-family: system-ui, -apple-system, Segoe UI, Roboto, sans-serif; margin: 0; padding: 24px; background: Canvas; color: CanvasText; }
  h1 { font-size: 1.25rem; margin: 0 0 16px; }
  .cards { display: flex; flex-wrap: wrap; gap: 12px; margin-bottom: 20px; }
  .card { flex: 1 1 160px; border: 1px solid color-mix(in srgb, CanvasText 15%, transparent); border-radius: 10px; padding: 14px 16px; }
  .card .n { font-size: 1.7rem; font-weight: 700; }
  .card .l { font-size: .8rem; opacity: .7; text-transform: uppercase; letter-spacing: .04em; }
  .bar { height: 10px; border-radius: 6px; background: color-mix(in srgb, CanvasText 12%, transparent); overflow: hidden; margin-top: 8px; }
  .bar > span { display: block; height: 100%; background: #2e8b57; }
  form { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin-bottom: 14px; }
  input, select, button { padding: 7px 10px; border-radius: 8px; border: 1px solid color-mix(in srgb, CanvasText 25%, transparent); background: Canvas; color: CanvasText; font-size: .9rem; }
  button { cursor: pointer; }
  .meta { font-size: .85rem; opacity: .7; margin-bottom: 8px; }
  table { border-collapse: collapse; width: 100%; font-size: .88rem; }
  th, td { text-align: left; padding: 7px 10px; border-bottom: 1px solid color-mix(in srgb, CanvasText 10%, transparent); vertical-align: top; }
  th { position: sticky; top: 0; background: Canvas; }
  tr:hover td { background: color-mix(in srgb, CanvasText 5%, transparent); }
  .noemail { opacity: .4; }
  .copy { padding: 1px 6px; font-size: .8rem; line-height: 1.2; border-radius: 6px; opacity: .5; }
  .copy:hover { opacity: 1; }
  .copy.ok { color: #2e8b57; border-color: #2e8b57; opacity: 1; }
  a { color: #3b82f6; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .wrap { overflow-x: auto; }
  .view { padding: 3px 10px; font-size: .82rem; border-radius: 6px; }
  .campaign-yes { color: #2e8b57; }
  .clamp { max-width: 340px; min-width: 220px; display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; cursor: pointer; }
  .clamp:hover { background: color-mix(in srgb, CanvasText 8%, transparent); }
  dialog#campaignModal { width: min(680px, 92vw); max-height: 85vh; border-radius: 12px; border: 1px solid color-mix(in srgb, CanvasText 20%, transparent); padding: 0; background: Canvas; color: CanvasText; }
  dialog#campaignModal::backdrop { background: rgba(0,0,0,.4); }
  .modal-inner { padding: 20px 24px; overflow-y: auto; max-height: 85vh; }
  .modal-inner h2 { margin: 0 0 2px; font-size: 1.1rem; }
  .modal-inner h3 { margin: 0 0 14px; font-weight: 500; opacity: .8; font-size: .95rem; }
  .modal-inner .label { font-size: .78rem; text-transform: uppercase; letter-spacing: .04em; opacity: .6; margin: 14px 0 4px; }
  .modal-inner pre { white-space: pre-wrap; font-family: inherit; margin: 0; line-height: 1.5; }
  #modalClose { float: right; border: none; background: none; font-size: 1.3rem; cursor: pointer; opacity: .6; padding: 4px 8px; }
  #modalClose:hover { opacity: 1; }
</style>
</head>
<body>
  <h1>krile — <code>{{.Table}}</code></h1>
  <div class="cards" id="cards">
    <div class="card"><div class="n">{{.Stats.Total}}</div><div class="l">Total</div></div>
    <div class="card"><div class="n">{{.Stats.WithEmail}}</div><div class="l">With email</div></div>
    <div class="card"><div class="n">{{.Stats.EmptyEmail}}</div><div class="l">Empty</div></div>
    <div class="card">
      <div class="n">{{printf "%.1f" .Stats.PctWithEmail}}%</div><div class="l">Coverage</div>
      <div class="bar"><span style="width:{{printf "%.1f" .Stats.PctWithEmail}}%"></span></div>
    </div>
  </div>

  <form method="get" action="/">
    <select name="filter">
      <option value="all"{{if eq .Filter "all"}} selected{{end}}>All</option>
      <option value="with"{{if eq .Filter "with"}} selected{{end}}>With email</option>
      <option value="empty"{{if eq .Filter "empty"}} selected{{end}}>Empty email</option>
    </select>
    <select name="category" title="Category">
      <option value="">All categories</option>
      {{range .Categories}}<option value="{{.}}"{{if eq . $.Category}} selected{{end}}>{{.}}</option>{{end}}
    </select>
    <select name="campaign" title="Email campaign">
      <option value="all"{{if eq .Campaign "all"}} selected{{end}}>Campaign: all</option>
      <option value="filled"{{if eq .Campaign "filled"}} selected{{end}}>Campaign: filled</option>
      <option value="empty"{{if eq .Campaign "empty"}} selected{{end}}>Campaign: not filled</option>
    </select>
    <input type="text" name="q" placeholder="search name or city" value="{{.Query}}">
    <input type="number" name="limit" min="1" max="5000" value="{{.Limit}}" title="max rows">
    <button type="submit">Apply</button>
  </form>

  <div id="live">
  <div class="meta">Showing {{.Shown}} row(s) (limit {{.Limit}}). <span id="ts"></span></div>
  <div class="wrap">
  <table>
    <thead><tr><th>ID</th><th>Name</th><th>Category</th><th>City</th><th>Phone</th><th>Website</th><th>Email</th><th>Reasoning</th><th>Personalized Email</th><th>Campaign</th></tr></thead>
    <tbody>
    {{range .Rows}}
      <tr>
        <td>{{.ID}}</td>
        <td><button class="copy" data-text="{{.Name}}" title="Copy name" aria-label="Copy name">⧉</button> {{.Name}}</td>
        <td>{{.Category}}</td>
        <td>{{.City}}</td>
        <td>{{.Phone}}</td>
        <td>{{if .Website}}<a href="{{.Website}}" target="_blank" rel="noopener">link</a>{{end}}</td>
        {{if .Email}}<td><a href="mailto:{{.Email}}">{{.Email}}</a></td>{{else}}<td class="noemail">—</td>{{end}}
        {{if .Reasoning}}
          <td class="clamp" data-name="{{.Name}}" data-subject="{{.Subject}}" data-products="{{.RelevantProducts}}" data-reasoning="{{.Reasoning}}" data-email="{{.PersonalizedEmail}}" title="Click to view full text">{{.Reasoning}}</td>
        {{else}}<td class="noemail">—</td>{{end}}
        {{if .PersonalizedEmail}}
          <td class="clamp" data-name="{{.Name}}" data-subject="{{.Subject}}" data-products="{{.RelevantProducts}}" data-reasoning="{{.Reasoning}}" data-email="{{.PersonalizedEmail}}" title="Click to view full text">{{.PersonalizedEmail}}</td>
        {{else}}<td class="noemail">—</td>{{end}}
        <td>
        {{if .PersonalizedEmail}}
          <button class="view campaign-yes" data-name="{{.Name}}" data-subject="{{.Subject}}" data-products="{{.RelevantProducts}}" data-reasoning="{{.Reasoning}}" data-email="{{.PersonalizedEmail}}">View</button>
        {{else}}<span class="noemail">—</span>{{end}}
        </td>
      </tr>
    {{else}}
      <tr><td colspan="10">No rows.</td></tr>
    {{end}}
    </tbody>
  </table>
  </div>
  </div>

  <dialog id="campaignModal">
    <div class="modal-inner">
      <button id="modalClose" aria-label="Close">×</button>
      <h2 id="modalName"></h2>
      <h3 id="modalSubject"></h3>
      <div class="label">Relevant products</div>
      <pre id="modalProducts"></pre>
      <div class="label">Reasoning</div>
      <pre id="modalReasoning"></pre>
      <div class="label">Email</div>
      <pre id="modalEmail"></pre>
    </div>
  </dialog>

  <script>
  // Auto-refresh stats + table every second without disturbing the form.
  async function refresh() {
    try {
      const res = await fetch(location.href, { headers: { "X-Partial": "1" } });
      if (!res.ok) return;
      const doc = new DOMParser().parseFromString(await res.text(), "text/html");
      for (const id of ["cards", "live"]) {
        const el = doc.getElementById(id);
        if (el) document.getElementById(id).innerHTML = el.innerHTML;
      }
      const ts = document.getElementById("ts");
      if (ts) ts.textContent = "· updated " + new Date().toLocaleTimeString();
    } catch (e) { /* keep trying */ }
  }
  setInterval(refresh, 1000);

  // Copy a name to the clipboard. Delegated so it survives the table refresh.
  document.addEventListener("click", async (e) => {
    const btn = e.target.closest(".copy");
    if (!btn) return;
    try {
      await navigator.clipboard.writeText(btn.dataset.text);
      const old = btn.textContent;
      btn.textContent = "✓";
      btn.classList.add("ok");
      setTimeout(() => { btn.textContent = old; btn.classList.remove("ok"); }, 900);
    } catch (err) { /* clipboard unavailable */ }
  });

  // Open the campaign modal with the row's full text. Delegated so it
  // survives the table refresh; the dialog itself lives outside #live.
  const modal = document.getElementById("campaignModal");
  document.addEventListener("click", (e) => {
    const btn = e.target.closest(".view, .clamp");
    if (!btn) return;
    document.getElementById("modalName").textContent = btn.dataset.name;
    document.getElementById("modalSubject").textContent = btn.dataset.subject;
    document.getElementById("modalProducts").textContent = btn.dataset.products;
    document.getElementById("modalReasoning").textContent = btn.dataset.reasoning;
    document.getElementById("modalEmail").textContent = btn.dataset.email;
    modal.showModal();
  });
  document.getElementById("modalClose").addEventListener("click", () => modal.close());
  modal.addEventListener("click", (e) => { if (e.target === modal) modal.close(); });
  </script>
</body>
</html>`
