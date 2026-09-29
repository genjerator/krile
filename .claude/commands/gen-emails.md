---
description: Research companies from the PostgreSQL companies table and write personalized cold-sales emails back into it (single company, an id range, or the next N unfilled rows)
argument-hint: <id> | "<company name>" | <id>-<id> | --limit N [--category "..."] [--refill]
---

## What this command does

Generates highly personalized B2B cold emails for companies stored in the local
`companies` PostgreSQL table (DB credentials in `.env`; parent table name
overridable via `--db-table` / `DB_TABLE`, defaults to `companies`), and writes
each generated email as a **new row in the `company_emails` table** — a
one-to-many child of `companies` (a company can have several generated emails).
Its columns: `id` (UUID PK, `gen_random_uuid()`), `company_id` (FK → `companies(id)`,
`ON DELETE CASCADE`), `relevant_products`, `reasoning`, `subject`,
`email_body` (all `TEXT`, nullable), plus the send-tracking columns
`email_status`, `email_sent_at`, `email_error` and `created_at`. The email
content no longer lives on the `companies` row.

If the `company_emails` table is somehow missing, create it with the exact
schema below (idempotent — `CREATE TABLE IF NOT EXISTS`; never
`DROP`/`ALTER TYPE`/anything destructive on existing tables):

```sql
CREATE TABLE IF NOT EXISTS company_emails (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  company_id         INTEGER NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  relevant_products  TEXT,
  reasoning          TEXT,
  subject            TEXT,
  email_body         TEXT,
  email_status       TEXT,
  email_sent_at      TIMESTAMPTZ,
  email_error        TEXT,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS company_emails_company_id_idx ON company_emails (company_id);
```

## Selecting target companies from $ARGUMENTS

Parse `$ARGUMENTS` to decide the mode:

"Already filled" now means the company already has at least one row in
`company_emails` (test with `EXISTS (SELECT 1 FROM company_emails e WHERE
e.company_id = c.id AND e.email_body IS NOT NULL AND
e.email_body <> '')`). Each generated email is a **new** row, so
re-processing a company adds another email rather than overwriting one.

- **A single integer** (e.g. `8354`) → one company, that `id`. Always process
  it, even if it already has an email (explicit request wins — adds a new row).
- **A quoted or bare name** (e.g. `"Neckarblick Hotel Garni"`) → look it up
  with `name ILIKE '%...%'`. If more than one row matches, list the matches
  (id, name, city) and ask which one before proceeding. Always (re)process
  the chosen company even if it already has an email.
- **An id range** `<id>-<id>` (e.g. `8300-8400`) → every company with `id` in
  that inclusive range. Always (re)process every one, even if already filled.
- **`--limit N`** (optionally with `--category "..."`) → the next `N`
  companies ordered by `id` that do **not** yet have any `company_emails` row
  with an `email_body` (i.e. resumable batch mode, skipping
  already-filled companies) — unless `--refill` is also passed, in which case
  ignore that check and take the next `N` companies by `id` regardless.
- No arguments → ask the user which mode they want (don't guess a default
  batch size).

Only select companies where `website IS NOT NULL AND website <> ''` — there's
nothing to research otherwise. Use `psql` with the credentials in `.env`
(`DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD`) to query and
later to write results back; `psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d
$DB_NAME` reading `.env` first.

## Per-company workflow (two stages, as requested)

**Research step:** For each selected company, fetch its `website` with
WebFetch to understand what it actually does — products/services, industry,
target customers, scale, and any signals about its food prep, storage, or
kitchen operations. Handle failures gracefully rather than blocking the batch:
redirects → refetch the redirect target; SSL errors, DNS failures, timeouts,
or bot-blocking 403s → treat as "insufficient public information" for that
row rather than retrying indefinitely or inventing detail.

Then decide which of the **real** Planeta Industries products are genuinely
relevant — **do not force a product that doesn't fit**. These are the only
product lines the shop actually sells (verified on planetaindustries.de);
never pitch anything else (in particular there are **no** ironing/mangel
systems and **no** ice-melt products — those were mistakes in an earlier
version of this command):

| Product line | When it fits | Link to insert (plain URL — UTM added at send time) |
|---|---|---|
| Vacuum packaging machines | Restaurants, butchers, caterers, hotels with kitchens — sous-vide, portioning, prepping ahead | `https://www.planetaindustries.de/de/categories/vakuum-maschinen` |
| — compact / tabletop chamber | Smaller kitchens, guesthouses | `https://www.planetaindustries.de/de/categories/c-serie` |
| — industrial / high volume | High-volume production | `https://www.planetaindustries.de/de/categories/p-serie` |
| — home / compact single unit | Very small operations | `https://www.planetaindustries.de/de/categories/haushalts-vakuumier-maschinen` |
| Vacuum bags & rolls | Anyone vacuum-sealing food for storage/freezing | `https://www.planetaindustries.de/de/categories/vakuumiertuten-rollen` |
| Spices & seasonings | Restaurants, butchers, caterers | `https://www.planetaindustries.de/de/categories/gewurze` |
| — spice blends | Consistent seasoning at volume | `https://www.planetaindustries.de/de/categories/gewuerzmischungen` |
| — marinades | Grills, butchers, catering | `https://www.planetaindustries.de/de/categories/marinaden` |

Insert 1–3 of these links inline in the email as HTML `<a href="...">` anchors
on natural anchor text (e.g. the product name), choosing the categories that
genuinely fit the business. Use plain URLs with **no** query params — the
`--send-emails` step appends `utm_*` tracking (including the per-email uuid as
`utm_content`) automatically when run with `--utm-campaign`. Before using any
link not in this table, verify it returns HTTP 200 — never invent product URLs.

If research turns up little or nothing usable (dead site, blocked, wrong/
mismatched category vs. the DB's `category` field), don't force a fit —
write a shorter, honest, more general email (still around the real products
above) that states limited public information was available, or, for a genuine
category mismatch, says so plainly rather than pretending a fit exists.

**Writing step:** Using the research output, write one unique email per
company that:

- Opens with a warm, human greeting (`Hello,` when no contact name is known)
  and a genuine, specific observation about the business — friendly and
  conversational, the way the MULTIVAC hotel example reads, not stiff or
  corporate.
- Mentions the company by name and demonstrates real understanding of what
  it does.
- Explains *why* the selected product(s) solve a concrete business problem
  for them — not just a feature list — and links the relevant product
  categories inline.
- Sounds like a natural, warm B2B email, not AI-generated or templated. Avoid
  stock phrases like "I hope this email finds you well."
- **Never uses an em dash.** Do not write `—` or `&mdash;`. Where you would join
  clauses with an em dash and a space (`— `), use a colon and a space (`: `)
  instead.
- Ends with one clear, low-pressure call to action (a quick look at the range,
  pricing/catalog by email, or a short call).
- Is 120–180 words, warm and helpful, fluent business English.
- Invents no facts — if something is uncertain, don't assume it.
- Is valid HTML (wrap paragraphs in `<p>`; links as `<a href>`), and signs off
  as `Planeta Industries` (no personal sender name unless one is provided).

Every email must read distinctly different from the others in the same run —
vary structure and phrasing, not just the mail-merged name.

## Writing results back to the database

For each processed company, `INSERT` a new row into `company_emails` using
dollar-quoted string literals (build the SQL in a small script — Python or a
heredoc — rather than hand-escaping quotes/apostrophes inline, since email
text will contain both):

```sql
INSERT INTO company_emails (company_id, relevant_products, reasoning, subject, email_body)
VALUES (<id>, $tag$...$tag$, $tag$...$tag$, $tag$...$tag$, $tag$...$tag$);
```

`<id>` is the `companies.id` (used as `company_id`). Leave `email_status` /
`email_sent_at` / `email_error` unset — the `--send-emails` step manages those.
Pick a dollar-quote tag that doesn't collide with the content (check first).
The `company_id` FK references the parent table from `--db-table`/`DB_TABLE` if
set, else `companies`.

## Reporting back

After the run, print a short summary table (id, name, whether research
succeeded or fell back to "limited info", subject) — not the full email
bodies inline, since those are now in the database and viewable via
`--serve`'s web UI (`localhost:8080`, filter "Campaign: filled", click
"View" on a row). Flag any row where the DB category looks mismatched with
what the website actually shows, the way "Glashauser" (tagged Fleischerei,
actually a Steuerberatung) was caught in an earlier run.
