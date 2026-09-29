#!/usr/bin/env python3
"""Load an email template file into a company_emails row.

The template file's first block (before a line containing only "---") is the
Subject; everything after is the HTML body. HTML comments (<!-- ... -->) are
stripped so editor notes never reach the recipient.

Usage:
    python3 scripts/apply_template.py templates/multivac-hotels.html <email-uuid>

DB credentials are read from .env (DB_HOST/DB_PORT/DB_NAME/DB_USER/DB_PASSWORD).
Runs psql; nothing is sent — use `krile --send-emails --email-id <uuid>` for that.
"""
import os
import re
import subprocess
import sys


def load_env(path=".env"):
    env = {}
    if not os.path.exists(path):
        return env
    for line in open(path, encoding="utf-8"):
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        env[k.strip()] = v.strip().strip('"').strip("'")
    return env


def parse_template(text):
    # Split on the first line that is exactly "---".
    parts = re.split(r"(?m)^---\s*$", text, maxsplit=1)
    if len(parts) != 2:
        sys.exit("template must have a '---' line separating Subject from body")
    head, body = parts
    m = re.search(r"(?im)^\s*Subject:\s*(.+)$", head)
    if not m:
        sys.exit("template head must contain a 'Subject:' line")
    subject = m.group(1).strip()
    body = re.sub(r"(?s)<!--.*?-->", "", body).strip()  # drop HTML comments
    return subject, body


def dollar_quote(s):
    # Pick a tag that does not appear in the content.
    for tag in ("$T$", "$TT$", "$TTT$", "$K1$", "$K2$"):
        if tag not in s:
            return tag + s + tag
    raise RuntimeError("could not find a safe dollar-quote tag")


def main():
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    tmpl_path, email_id = sys.argv[1], sys.argv[2]
    if not re.match(
        r"(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
        email_id,
    ):
        sys.exit(f"not a valid uuid: {email_id!r}")

    subject, body = parse_template(open(tmpl_path, encoding="utf-8").read())
    env = load_env()
    table = os.environ.get("EMAILS_TABLE", "company_emails")
    sql = (
        f"UPDATE {table} SET subject = {dollar_quote(subject)}, "
        f"personalized_email = {dollar_quote(body)} "
        f"WHERE id = '{email_id}';"
    )

    cmd = [
        "psql",
        "-h", env.get("DB_HOST", "localhost"),
        "-p", env.get("DB_PORT", "5432"),
        "-U", env.get("DB_USER", ""),
        "-d", env.get("DB_NAME", ""),
        "-v", "ON_ERROR_STOP=1",
        "-c", sql,
    ]
    penv = dict(os.environ, PGPASSWORD=env.get("DB_PASSWORD", ""))
    res = subprocess.run(cmd, env=penv)
    if res.returncode != 0:
        sys.exit(res.returncode)
    print(f"applied template {tmpl_path!r} to {table} id={email_id}")
    print(f"  subject: {subject}")


if __name__ == "__main__":
    main()
