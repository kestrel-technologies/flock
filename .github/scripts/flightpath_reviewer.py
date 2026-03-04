#!/usr/bin/env python3
"""Flightpath Reviewer v2.3: automated KES review for one CL (pull request).

Runs as the `flightpath/reviewer` check on every CL revision (Flightpath §6, KES §5).
1. Reads the CL diff and the post-change text of every changed file (< 200 KB, text only).
2. Reads KES from KES_URL (fallback: .github/kes/KES-3.md if vendored).
3. Asks the review model for a verdict per KES clause (JSON), temperature 0.
4. Upserts one CL comment marked <!-- flightpath-reviewer --> with the verdict table.
5. Sets commit status `flightpath/reviewer`: success (no Block), failure (any Block not
   overridden by a code owner with `flightpath: override <clause>`), error (reviewer unavailable).

The Reviewer is a reviewer, not an approver (KES §5 rule 1).
Identical copy in every code repository; do not edit here, edit the Roost source and re-sync.
"""
import json
import os
import re
import sys
from pathlib import Path

import requests

VERSION = "2.3"
KES_REVISION = "KES-3.0"
MODEL = "claude-sonnet-5-5"
MARKER = "<!-- flightpath-reviewer -->"
CONTEXT = "flightpath/reviewer"
MAX_FILE_BYTES = 200_000
MAX_INPUT_TOKENS = 60_000
SKIP_FILES = re.compile(r"(\.lock|go\.sum|package-lock\.json|\.png|\.jpg|\.jpeg|\.gif|\.ico|\.pdf|\.zip)$")
CLAUSES = ["KES-SEC-1", "KES-SEC-2", "KES-SEC-3", "KES-SEC-4", "KES-SEC-5",
           "KES-DEV-1", "KES-DEV-2", "KES-DEV-3", "KES-DEV-4", "KES-DEV-5", "KES-DEV-6", "KES-DEP-1"]
SYSTEM = (
    "You are the Flightpath Reviewer. Evaluate the change against each KES clause. "
    "Output JSON: [{clause, verdict: Pass|Warn|Block|n/a, file, line, finding, fix}]. "
    "Block only on clear violations visible in the diff or changed files. "
    "Do not speculate about files you were not given."
)

API = os.environ.get("GITHUB_API_URL", "https://api.github.com")
REPO = os.environ.get("GITHUB_REPOSITORY", "")
TOKEN = os.environ.get("GITHUB_TOKEN", "")
S = requests.Session()
S.headers.update({"Authorization": f"Bearer {TOKEN}", "Accept": "application/vnd.github+json",
                  "X-GitHub-Api-Version": "2022-11-28"})


def gh(method, path, **kw):
    r = S.request(method, f"{API}{path}", timeout=30, **kw)
    r.raise_for_status()
    return r


def set_status(sha, state, description):
    gh("POST", f"/repos/{REPO}/statuses/{sha}",
       json={"state": state, "context": CONTEXT, "description": description[:140]})


def upsert_comment(number, body):
    body = f"{MARKER}\n{body}"
    comments = gh("GET", f"/repos/{REPO}/issues/{number}/comments", params={"per_page": 100}).json()
    for c in comments:
        if MARKER in (c.get("body") or ""):
            gh("PATCH", f"/repos/{REPO}/issues/comments/{c['id']}", json={"body": body})
            return
    gh("POST", f"/repos/{REPO}/issues/{number}/comments", json={"body": body})


def changed_files(number, head_sha):
    out = []
    files = gh("GET", f"/repos/{REPO}/pulls/{number}/files", params={"per_page": 100}).json()
    for f in files:
        if f["status"] == "removed" or SKIP_FILES.search(f["filename"]):
            continue
        r = S.get(f"{API}/repos/{REPO}/contents/{f['filename']}", params={"ref": head_sha},
                  headers={"Accept": "application/vnd.github.raw+json"}, timeout=30)
        if r.status_code != 200 or len(r.content) > MAX_FILE_BYTES:
            continue
        try:
            out.append((f["filename"], r.content.decode("utf-8")))
        except UnicodeDecodeError:
            continue
    return out


def load_kes():
    url = os.environ.get("KES_URL")
    if url:
        try:
            r = requests.get(url, timeout=30)
            if r.status_code == 200 and "KES-SEC-1" in r.text:
                return r.text
        except requests.RequestException:
            pass
    vendored = Path(".github/kes/KES-3.md")
    return vendored.read_text(encoding="utf-8") if vendored.exists() else ""


def overrides(number):
    found = set()
    for c in gh("GET", f"/repos/{REPO}/issues/{number}/comments", params={"per_page": 100}).json():
        for m in re.finditer(r"flightpath: override (KES-[A-Z]+-\d+)", c.get("body") or ""):
            found.add(m.group(1))
    return found


def parse_verdicts(text):
    text = text.strip()
    text = re.sub(r"^```(?:json)?\s*|\s*```$", "", text)
    data = json.loads(text)
    if not isinstance(data, list):
        raise ValueError("verdicts must be a list")
    for row in data:
        if row.get("verdict") not in ("Pass", "Warn", "Block", "n/a") or not str(row.get("clause", "")).startswith("KES-"):
            raise ValueError(f"bad row: {row}")
    return data


def review(kes, diff, files):
    import anthropic  # pinned in the workflow's pip install line

    client = anthropic.Anthropic(api_key=os.environ["ANTHROPIC_API_KEY"])
    changed = "\n\n".join(f"=== {name} ===\n{text}" for name, text in files)
    user = (f"Clauses to evaluate: {', '.join(CLAUSES)}\n\n## Diff\n{diff}\n\n"
            f"## Changed files (post-change text)\n{changed}\n\nReturn only the JSON array.")
    last_err = None
    for _ in range(2):
        msg = client.messages.create(
            model=MODEL, max_tokens=4000, temperature=0,
            system=[{"type": "text", "text": SYSTEM},
                    {"type": "text", "text": f"# KES\n{kes}", "cache_control": {"type": "ephemeral"}}],
            messages=[{"role": "user", "content": user}],
        )
        try:
            return parse_verdicts("".join(b.text for b in msg.content if b.type == "text"))
        except (ValueError, json.JSONDecodeError) as e:
            last_err = e
    raise RuntimeError(f"invalid reviewer output: {last_err}")


def table(rows):
    out = ["| Clause | Verdict | Finding | Fix |", "|---|---|---|---|"]
    for r in sorted(rows, key=lambda r: CLAUSES.index(r["clause"]) if r["clause"] in CLAUSES else 99):
        where = f"`{r.get('file')}`:{r.get('line')} " if r.get("file") else ""
        finding = (where + (r.get("finding") or "")).replace("|", "\\|").replace("\n", " ")
        fix = (r.get("fix") or "").replace("|", "\\|").replace("\n", " ")
        out.append(f"| {r['clause']} | **{r['verdict']}** | {finding} | {fix} |")
    return "\n".join(out)


def footer():
    return f"\n\n<sub>Flightpath Reviewer v{VERSION} · {KES_REVISION} · model {MODEL}</sub>"


def main():
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    pr = event["pull_request"]
    number, sha = pr["number"], pr["head"]["sha"]
    set_status(sha, "pending", "Flightpath Reviewer running")
    try:
        diff = gh("GET", f"/repos/{REPO}/pulls/{number}", headers={"Accept": "application/vnd.github.v3.diff"}).text
        files = changed_files(number, sha)
        kes = load_kes()
        if not kes or not os.environ.get("ANTHROPIC_API_KEY"):
            raise RuntimeError("standards text or reviewer credentials unavailable")
        approx_tokens = (len(diff) + sum(len(t) for _, t in files) + len(kes)) // 4
        if approx_tokens > MAX_INPUT_TOKENS:
            upsert_comment(number, "## Flightpath Reviewer\n\ndiff too large for automated review "
                                   "(>60k tokens); human review required." + footer())
            set_status(sha, "error", "diff too large for automated review; human review required")
            return 0
        rows = review(kes, diff, files)
    except Exception as e:  # noqa: BLE001 - any failure is reported, never silently passed
        print(f"::warning::Flightpath Reviewer unavailable: {e}")
        upsert_comment(number, "## Flightpath Reviewer\n\nReviewer unavailable; human review required." + footer())
        set_status(sha, "error", "Reviewer unavailable")
        return 0
    ov = overrides(number)
    blocks = [r for r in rows if r["verdict"] == "Block" and r["clause"] not in ov]
    summary = (f"**{len(blocks)} Block**" if blocks else "No Block") + \
              (f" · overrides: {', '.join(sorted(ov))}" if ov else "")
    upsert_comment(number, f"## Flightpath Reviewer\n\n{summary}\n\n{table(rows)}{footer()}")
    set_status(sha, "failure" if blocks else "success",
               f"{len(blocks)} blocking finding(s)" if blocks else "no blocking findings")
    return 0


if __name__ == "__main__":
    sys.exit(main())
