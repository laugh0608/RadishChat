#!/usr/bin/env python3
"""Validate the RadishChat repository contract and optional PR commit range."""

import argparse
from collections import Counter
import json
from pathlib import Path
import re
import subprocess
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = (
    "README.md", "AGENTS.md", "CLAUDE.md", "CONTRIBUTING.md", "SECURITY.md",
    "CODE_OF_CONDUCT.md", ".editorconfig", ".gitattributes", ".gitignore",
    ".github/PULL_REQUEST_TEMPLATE.md", ".github/ISSUE_TEMPLATE/config.yml",
    ".github/ISSUE_TEMPLATE/bug-report.yml", ".github/ISSUE_TEMPLATE/change-proposal.yml",
    ".github/rulesets/README.md", ".github/rulesets/master-protection.json",
    ".github/workflows/pr-check.yml", "web/package.json", "docs/README.md", "docs/status/current.md",
    "docs/planning/project-plan.md", "docs/topics/README.md",
    "docs/governance/collaboration.md", "docs/governance/repository.md",
    "docs/adr/0002-branch-pr-rulesets.md",
)
INLINE_LINK = re.compile(r"!?(?<!\\)\[[^\[\]\n]+\]\(<?([^\s)>]+)>?(?:[ \t]+['\"].*?['\"])?\)")
HTML_ANCHOR = re.compile(r'\bid=["\']([^"\']+)["\']')
COMMIT = re.compile(
    r"(?:feat|fix|docs|refactor|test|chore|ci|build|perf|revert)"
    r"(?:\([a-z0-9._/-]+\))?!?: \S.*"
)
ROUTE = re.compile(r"^## ([A-Z]+-\d+) .+", re.MULTILINE)
SKIP = {"archive", "records", "node_modules", "dist"}


def active_markdown():
    yield from sorted(ROOT.glob("*.md"))
    for area in ("docs", ".github"):
        for path in sorted((ROOT / area).rglob("*.md")):
            if not SKIP.intersection(path.relative_to(ROOT).parts):
                yield path


def read_text(path):
    return path.read_bytes().decode("utf-8")


def outside_code(text):
    fence = None
    for line in text.splitlines():
        marker = re.match(r"^\s{0,3}(`{3,}|~{3,})", line)
        if marker:
            sequence = marker.group(1)
            if fence is None:
                fence = sequence
            elif sequence.startswith(fence):
                fence = None
            continue
        if fence is None:
            yield line


def anchors(text):
    found = set(HTML_ANCHOR.findall(text))
    counts = Counter()
    for line in outside_code(text):
        match = re.match(r"^#{1,6}\s+(.+?)\s*#*$", line)
        if match:
            heading = re.sub(r"[^\w\- ]", "", match[1].lower()).replace(" ", "-")
            found.add(f"{heading}-{counts[heading]}" if counts[heading] else heading)
            counts[heading] += 1
    return found


def link_problems(path, text):
    for line in outside_code(text):
        for match in INLINE_LINK.finditer(line):
            link = match[1]
            parsed = urlsplit(link)
            if parsed.scheme or parsed.netloc or parsed.path.startswith("/"):
                continue
            target = (path.parent / unquote(parsed.path)).resolve() if parsed.path else path
            if not target.is_relative_to(ROOT) or not target.exists():
                yield f"{path.relative_to(ROOT)}: unresolved link {link}"
            elif parsed.fragment and target.suffix == ".md":
                if unquote(parsed.fragment) not in anchors(read_text(target)):
                    yield f"{path.relative_to(ROOT)}: unresolved anchor {link}"


def workflow_problems(workflow):
    header, _, jobs = workflow.partition("\njobs:\n")
    if not re.search(r"(?m)^  pull_request:\n    branches:\n      - dev\n      - master$", header):
        yield "PR Checks must target dev and master."
    if re.search(r"(?m)^  (push|pull_request_target):", header):
        yield "PR Checks must run only in its declared pull request context."
    if not re.search(r"(?m)^permissions:\n  contents: read$", header):
        yield "PR Checks must declare read-only content permissions."

    job_blocks = re.split(r"(?m)^  ([a-z][a-z0-9-]*):\n", jobs)[1:]
    jobs_by_id = dict(zip(job_blocks[::2], job_blocks[1::2]))
    gate_results = {"repo-hygiene": "HYGIENE", "go-checks": "GO_CHECKS", "web-checks": "WEB_CHECKS"}
    dependencies = ", ".join(gate_results)
    success_tests = " && ".join(f'test "${variable}" = success' for variable in gate_results.values())
    candidate = jobs_by_id.get("candidate-quality", "")
    gate_lines = {
        "    name: Candidate Quality",
        f"    needs: [{dependencies}]",
        "    if: ${{ always() }}",
        "        if: ${{ always() }}",
        f"        run: {success_tests}",
        *(f"          {variable}: ${{{{ needs.{job}.result }}}}" for job, variable in gate_results.items()),
    }
    masks_failure = re.compile(r"(?m)^ +(?:- )?continue-on-error:(?! false$)")
    if not gate_lines.issubset(candidate.splitlines()) or masks_failure.search(candidate):
        yield "Candidate Quality must always run and require success from Repo Hygiene, Go Checks, and Web Checks."

    required_commands = {
        "repo-hygiene": ('python3 scripts/check-repo.py --base "$PR_BASE" --head "$PR_HEAD"',),
        "go-checks": ("go test -mod=readonly -count=1 -race ./...", "go vet -mod=readonly ./..."),
        "web-checks": ("npm ci --no-audit --no-fund", "npm run build"),
    }
    for job, commands in required_commands.items():
        body = jobs_by_id.get(job, "")
        if (
            not {f"        run: {command}" for command in commands}.issubset(body.splitlines())
            or re.search(r"(?m)^ +(?:- )?if:", body)
            or masks_failure.search(body)
        ):
            yield f"PR Checks job {job} must run its required commands and report failures."


def governance_problems():
    ruleset = json.loads(read_text(ROOT / ".github/rulesets/master-protection.json"))
    rules = {rule["type"]: rule.get("parameters", {}) for rule in ruleset["rules"]}
    pr = rules.get("pull_request", {})
    required = rules.get("required_status_checks", {})
    if (
        ruleset["target"] != "branch" or ruleset["enforcement"] != "active"
        or ruleset["bypass_actors"] != []
        or ruleset["conditions"]["ref_name"] != {"include": ["refs/heads/master"], "exclude": []}
        or not {"deletion", "non_fast_forward"}.issubset(rules)
        or pr.get("allowed_merge_methods") != ["merge", "rebase"]
        or pr.get("required_approving_review_count") != 0
        or pr.get("required_review_thread_resolution") is not True
        or required.get("required_status_checks") != [{"context": "Candidate Quality"}]
        or required.get("strict_required_status_checks_policy") is not True
    ):
        yield "Ruleset differs from the documented master branch contract."

    yield from workflow_problems(read_text(ROOT / ".github/workflows/pr-check.yml"))

    web_package = json.loads(read_text(ROOT / "web/package.json"))
    if web_package.get("scripts", {}).get("build") != "tsc --noEmit && vite build":
        yield "The Web build must type-check with TypeScript before running the Vite production build."

    if read_text(ROOT / "AGENTS.md") != read_text(ROOT / "CLAUDE.md"):
        yield "AGENTS.md and CLAUDE.md must have identical contents."

    topics = ROOT / "docs/topics"
    index = read_text(topics / "README.md")
    owners = {}
    for path in sorted(topics.glob("*.md")):
        if path.name == "README.md":
            continue
        for item in ROUTE.findall(read_text(path)):
            if item in owners:
                yield f"{item} has multiple owners: {owners[item]} and {path.name}."
            owners[item] = path.name
            anchor = f"{path.name}#{item.lower()}"
            if index.count(f"]({anchor})") != 1:
                yield f"{item} must have one link to its owner in the topic index."


def git(*args):
    return subprocess.run(
        ["git", "-C", str(ROOT), *args], check=True, text=True, capture_output=True,
    ).stdout


def git_problems(base, head):
    for ref in (base, head):
        if not re.fullmatch(r"[0-9a-fA-F]{7,64}", ref):
            raise ValueError("PR base and head must be commit SHAs.")
    point = git("merge-base", base, head).strip()
    git("diff", "--check", point, head)
    for line in git("log", "--no-merges", "--format=%h%x09%s", f"{point}..{head}").splitlines():
        sha, subject = line.split("\t", 1)
        if not COMMIT.fullmatch(subject):
            yield f"{sha}: expected Conventional Commit subject: {subject}"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base")
    parser.add_argument("--head")
    args = parser.parse_args()
    if (args.base is None) != (args.head is None):
        parser.error("--base and --head must be provided together")

    problems = []
    paths = set(active_markdown())
    for name in REQUIRED:
        path = ROOT / name
        if path.is_file():
            paths.add(path)
        else:
            problems.append(f"Missing required file: {name}")

    try:
        for path in sorted(paths):
            content = read_text(path)
            if content.startswith("\ufeff") or "\r" in content or not content.endswith("\n"):
                problems.append(f"{path.relative_to(ROOT)}: expected UTF-8, LF and final newline")
            if path.suffix == ".md":
                problems.extend(link_problems(path, content))
        if not problems:
            problems.extend(governance_problems())
        if args.base:
            problems.extend(git_problems(args.base, args.head))
    except subprocess.CalledProcessError as exc:
        problems.append((exc.stdout or exc.stderr or str(exc)).strip())
    except (OSError, ValueError, KeyError, TypeError) as exc:
        problems.append(str(exc))

    for issue in problems:
        print(issue)
    if problems:
        return 1
    print("RadishChat repository checks passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
