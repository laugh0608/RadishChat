#!/usr/bin/env python3
"""Validate the RadishChat planning baseline and optional PR commit range."""

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
    ".github/workflows/pr-check.yml", "docs/README.md", "docs/status/current.md",
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

    workflow = read_text(ROOT / ".github/workflows/pr-check.yml")
    header = workflow.split("\njobs:\n", 1)[0]
    if not re.search(r"(?m)^  pull_request:\n    branches:\n      - dev\n      - master$", header):
        yield "PR Checks must target dev and master."
    if re.search(r"(?m)^  (push|pull_request_target):", header):
        yield "PR Checks must run only in its declared pull request context."
    if not re.search(r"(?m)^permissions:\n  contents: read$", header):
        yield "PR Checks must declare read-only content permissions."
    candidate = workflow.partition("\n  candidate-quality:\n")[2]
    if not all(part in candidate for part in (
        "name: Candidate Quality", "needs: repo-hygiene", "always()",
        "needs.repo-hygiene.result", 'test "$HYGIENE" = success',
    )):
        yield "Candidate Quality must wait for and require Repo Hygiene."

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
