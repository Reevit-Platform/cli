#!/usr/bin/env bash
#
# release-sdks.sh — assess and orchestrate releases across every Reevit SDK.
#
# For each SDK submodule it answers, from ground truth (git + each registry):
#   • is the working tree clean, and is the local branch pushed?
#   • what version is on the default branch vs. what's published?
#   • is a release needed, and is the SDK in a safe state to release?
#
# With --execute it then, per release-worthy SDK: pushes the default branch,
# creates a GitHub Release (which triggers the publish workflow), watches that
# workflow to success, and verifies the new version actually appears on the
# registry. Without --execute it only prints the plan and changes nothing.
#
# Usage:
#   scripts/release-sdks.sh                 # dry-run: assess all SDKs, print plan
#   scripts/release-sdks.sh python node     # dry-run, limited to named SDKs
#   scripts/release-sdks.sh --bump react:patch --bump php:minor
#                                            # dry-run explicit version bumps
#   scripts/release-sdks.sh --execute       # perform pushes/releases/publishes
#   scripts/release-sdks.sh --execute --bump react:patch
#                                            # bump, commit, push, and publish
#   scripts/release-sdks.sh --execute --yes # ...without the interactive confirm
#
# Exit status: 0 if the plan is clean / all actions succeeded; 1 if any SDK is
# blocked (dirty tree, unmerged work) or any executed release failed.

set -uo pipefail

# ── config: key | path | owner/repo | kind | registry-package | manifest ──────
# kind ∈ {npm, pypi, go, packagist, crates}. Go and Rust tags use "v".
SDKS=(
  "core|sdks/core|Reevit-Platform/core-sdk|npm|@reevit/core|package.json"
  "react|sdks/react|Reevit-Platform/react-sdk|npm|@reevit/react|package.json"
  "svelte|sdks/svelte|Reevit-Platform/svelte-sdk|npm|@reevit/svelte|package.json"
  "node|sdks/typescript|Reevit-Platform/node-sdk|npm|@reevit/node|package.json"
  "vue|sdks/vue|Reevit-Platform/vue-sdk|npm|@reevit/vue|package.json"
  "python|sdks/python|Reevit-Platform/python-sdk|pypi|reevit|reevit/_version.py"
  "go|sdks/go|Reevit-Platform/go-sdk|go|github.com/reevit-platform/go-sdk|client.go"
  "php|sdks/php|Reevit-Platform/php-sdk|packagist|reevit/reevit-php|composer.json"
  "rust|sdks/rust|Reevit-Platform/rust-sdk|crates|reevit|Cargo.toml"
)

# ── flags ─────────────────────────────────────────────────────────────────────
EXECUTE=0
ASSUME_YES=0
FILTER=()
BUMP_SPECS=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    --execute) EXECUTE=1 ;;
    --yes|-y)  ASSUME_YES=1 ;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    --bump)
      [ "$#" -ge 2 ] || { echo "--bump requires SDK:LEVEL" >&2; exit 2; }
      BUMP_SPECS+=("$2"); shift ;;
    --bump=*)  BUMP_SPECS+=("${1#--bump=}") ;;
    -*)        echo "unknown flag: $1" >&2; exit 2 ;;
    *)         FILTER+=("$1") ;;
  esac
  shift
done

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

# ── colors (only when writing to a tty) ───────────────────────────────────────
if [ -t 1 ]; then
  BOLD=$'\033[1m'; DIM=$'\033[2m'; RED=$'\033[31m'; GRN=$'\033[32m'
  YLW=$'\033[33m'; BLU=$'\033[34m'; RST=$'\033[0m'
else
  BOLD=; DIM=; RED=; GRN=; YLW=; BLU=; RST=
fi
say()     { printf '%s\n' "$*"; }
section() { printf '\n%s%s%s\n' "$BOLD" "$*" "$RST"; }

sdk_exists() {
  local wanted="$1" entry key
  for entry in "${SDKS[@]}"; do
    IFS='|' read -r key _ <<<"$entry"
    [ "$key" = "$wanted" ] && return 0
  done
  return 1
}

array_contains() {
  local wanted="$1" item; shift
  for item in "$@"; do [ "$item" = "$wanted" ] && return 0; done
  return 1
}

BUMP_KEYS=()
BUMP_LEVELS=()
for spec in ${BUMP_SPECS[@]+"${BUMP_SPECS[@]}"}; do
  key="${spec%%:*}"; level="${spec#*:}"
  [ "$key" != "$level" ] || { echo "invalid --bump '$spec'; expected SDK:LEVEL" >&2; exit 2; }
  sdk_exists "$key" || { echo "unknown SDK in --bump: $key" >&2; exit 2; }
  case "$level" in patch|minor|major) ;; *) echo "invalid bump level for $key: $level" >&2; exit 2 ;; esac
  array_contains "$key" ${BUMP_KEYS[@]+"${BUMP_KEYS[@]}"} && { echo "duplicate --bump for $key" >&2; exit 2; }
  BUMP_KEYS+=("$key"); BUMP_LEVELS+=("$level")
  array_contains "$key" ${FILTER[@]+"${FILTER[@]}"} || FILTER+=("$key")
done

for key in ${FILTER[@]+"${FILTER[@]}"}; do
  sdk_exists "$key" || { echo "unknown SDK filter: $key" >&2; exit 2; }
done

bump_level_for() {
  local wanted="$1" index
  for ((index=0; index<${#BUMP_KEYS[@]}; index++)); do
    [ "${BUMP_KEYS[$index]}" = "$wanted" ] && { printf '%s' "${BUMP_LEVELS[$index]}"; return 0; }
  done
  return 1
}

# ── version helpers ───────────────────────────────────────────────────────────
strip_v() { printf '%s' "${1#v}"; }
next_version() { python3 "$ROOT/scripts/sdk_release_versions.py" next "$1" "$2"; }

# a < b  (semver), using version sort; equal → false
ver_lt() {
  [ "$1" = "$2" ] && return 1
  [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | command head -1)" = "$1" ]
}

# read the release version from a manifest at a git ref, without checkout.
# args: kind dir manifest ref  → prints bare version (no leading v) or "" if none
version_at_ref() {
  local kind="$1" dir="$2" manifest="$3" ref="$4" blob
  blob="$(git -C "$dir" show "$ref:$manifest" 2>/dev/null)" || { printf ''; return; }
  case "$kind" in
    npm)
      printf '%s' "$blob" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("version",""))' 2>/dev/null ;;
    pypi)
      printf '%s' "$blob" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.]+)?' | command head -1 ;;
    go)
      printf '%s' "$blob" | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | command head -1 | sed 's/^v//' ;;
    packagist)
      # composer.json usually has no version field (Packagist derives from tags)
      printf '%s' "$blob" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("version",""))' 2>/dev/null ;;
    crates)
      printf '%s' "$blob" | sed -n 's/^version = "\([^"]*\)"/\1/p' | command head -1 ;;
  esac
}

# highest published version on the registry. prints bare version or "" if none.
published_version() {
  local kind="$1" pkg="$2"
  case "$kind" in
    npm)
      npm view "$pkg" version 2>/dev/null ;;
    pypi)
      curl -fsS "https://pypi.org/pypi/$pkg/json" 2>/dev/null \
        | python3 -c 'import sys,json;print(json.load(sys.stdin)["info"]["version"])' 2>/dev/null ;;
    go)
      curl -fsS "https://proxy.golang.org/$pkg/@latest" 2>/dev/null \
        | python3 -c 'import sys,json;print(json.load(sys.stdin)["Version"].lstrip("v"))' 2>/dev/null ;;
    packagist)
      curl -fsS "https://packagist.org/packages/$pkg.json" 2>/dev/null \
        | python3 -c 'import sys,json
d=json.load(sys.stdin)["package"]["versions"]
vs=[v for v in d if "dev" not in v and "-" not in v]
print(vs[0].lstrip("v") if vs else "")' 2>/dev/null ;;
    crates)
      curl -fsS -A 'reevit-release-orchestrator/1.0 (+https://github.com/Reevit-Platform)' \
        "https://crates.io/api/v1/crates/$pkg" 2>/dev/null \
        | python3 -c 'import sys,json;print(json.load(sys.stdin)["crate"].get("max_stable_version", ""))' 2>/dev/null ;;
  esac
}

# ── per-SDK assessment (writes A_* globals) ───────────────────────────────────
A_default=main; A_dirty=0; A_ahead=0; A_behind=0; A_localv=; A_pubv=; A_nextv=; A_verdict=; A_detail=; A_branch=

assess() {
  local key="$1" dir="$2" repo="$3" kind="$4" pkg="$5" manifest="$6"
  A_default=main; A_dirty=0; A_ahead=0; A_behind=0; A_localv=; A_pubv=; A_nextv=; A_verdict=; A_detail=; A_branch=

  git -C "$dir" fetch --quiet origin --tags 2>/dev/null || true
  A_default="$(gh api "repos/$repo" --jq .default_branch 2>/dev/null || echo main)"
  A_branch="$(git -C "$dir" branch --show-current 2>/dev/null)"
  A_dirty="$(git -C "$dir" status --porcelain 2>/dev/null | grep -c . || true)"

  # Is the current branch ahead/behind its upstream? Bumps require a clean,
  # synchronized checkout of the repository's default branch.
  local up
  if [ "$A_branch" = "$A_default" ]; then
    up="origin/$A_default"
  else
    up="$(git -C "$dir" rev-parse --abbrev-ref --symbolic-full-name '@{upstream}' 2>/dev/null || true)"
  fi
  if [ -n "$up" ]; then
    A_ahead="$(git -C "$dir" rev-list --count "${up}..HEAD" 2>/dev/null || echo 0)"
    A_behind="$(git -C "$dir" rev-list --count "HEAD..${up}" 2>/dev/null || echo 0)"
  fi

  A_localv="$(version_at_ref "$kind" "$dir" "$manifest" "origin/$A_default")"
  # go/php have no manifest version → fall back to highest release tag on default
  if [ -z "$A_localv" ]; then
    A_localv="$(git -C "$dir" tag --merged "origin/$A_default" --sort=-v:refname 2>/dev/null \
                | grep -E '^v?[0-9]+\.[0-9]+\.[0-9]+$' | command head -1 | sed 's/^v//')"
  fi
  A_pubv="$(published_version "$kind" "$pkg")"

  # verdict precedence: dirty tree first (safety), then release decision.
  if [ "${A_dirty:-0}" -gt 0 ]; then
    A_verdict=BLOCKED; A_detail="${A_dirty} uncommitted file(s) — commit or discard before releasing"
  elif [ -z "$A_localv" ]; then
    A_verdict=UNKNOWN; A_detail="no version found on origin/$A_default (tag-driven) — release manually"
  elif [ -z "$A_pubv" ]; then
    A_verdict=RELEASE; A_detail="not yet on registry → first publish of v$A_localv"
  elif ver_lt "$A_pubv" "$A_localv"; then
    A_verdict=RELEASE; A_detail="registry $A_pubv → release $A_localv"
  elif ver_lt "$A_localv" "$A_pubv"; then
    A_verdict=BEHIND; A_detail="registry ($A_pubv) is AHEAD of origin/$A_default ($A_localv) — investigate"
  else
    A_verdict=CURRENT; A_detail="v$A_localv already published"
  fi
}

plan_bump() {
  local key="$1" dir="$2" kind="$3" level="$4" immediate_next current_tag
  if [ "${A_dirty:-0}" -gt 0 ]; then
    A_verdict=BLOCKED; A_detail="cannot bump with ${A_dirty} uncommitted file(s)"
  elif [ "$A_branch" != "$A_default" ]; then
    A_verdict=BLOCKED; A_detail="checkout $A_default before bumping (currently on ${A_branch:-detached HEAD})"
  elif [ "${A_behind:-0}" -gt 0 ]; then
    A_verdict=BLOCKED; A_detail="local $A_default is behind origin/$A_default by $A_behind commit(s)"
  elif [ "${A_ahead:-0}" -gt 0 ]; then
    A_verdict=BLOCKED; A_detail="push the $A_ahead existing commit(s) on $A_default before bumping"
  elif [ "$A_verdict" = RELEASE ] && current_tag="$(tag_for "$kind" "$A_localv")" && \
      ! git -C "$dir" ls-remote --tags origin "refs/tags/$current_tag" 2>/dev/null | grep -q .; then
    A_detail="cannot bump while unpublished v$A_localv has no release tag"
    A_verdict=BLOCKED
  elif [ "$A_verdict" != CURRENT ] && [ "$A_verdict" != RELEASE ]; then
    A_detail="cannot bump from $A_verdict — ${A_detail}"
    A_verdict=BLOCKED
  elif ! immediate_next="$(next_version "$A_localv" "$level")" || \
      ! A_nextv="$(next_available_version "$dir" "$kind" "$A_localv" "$level")"; then
    A_verdict=BLOCKED; A_detail="could not calculate $level bump from $A_localv"
  else
    A_verdict=BUMP
    A_detail="$level bump v$A_localv → v$A_nextv; commit, push, release, and publish"
    [ "$A_nextv" != "$immediate_next" ] && A_detail+=" (skipped occupied tag $(tag_for "$kind" "$immediate_next"))"
  fi
}

# ── release execution (only under --execute) ──────────────────────────────────
tag_for() { case "$1" in go|crates) printf 'v%s' "$2" ;; *) printf '%s' "$2" ;; esac; }

next_available_version() {
  local dir="$1" kind="$2" current="$3" level="$4" candidate tag
  candidate="$(next_version "$current" "$level")" || return 1
  while :; do
    tag="$(tag_for "$kind" "$candidate")"
    if ! git -C "$dir" ls-remote --tags origin "refs/tags/$tag" 2>/dev/null | grep -q .; then
      printf '%s' "$candidate"
      return 0
    fi
    candidate="$(next_version "$candidate" "$level")" || return 1
  done
}

# poll a registry until it serves $want (bare version) or times out. 0 ok / 1 timeout.
wait_for_registry() {
  local kind="$1" pkg="$2" want="$3" tries=40 got
  while [ "$tries" -gt 0 ]; do
    got="$(published_version "$kind" "$pkg")"
    [ "$(strip_v "$got")" = "$(strip_v "$want")" ] && return 0
    sleep 15; tries=$((tries-1))
  done
  return 1
}

publish_run_for_tag() {
  gh run list --repo "$1" --workflow publish.yml --event release \
    --branch "$2" --limit 1 --json databaseId --jq '.[0].databaseId' 2>/dev/null || true
}

watch_run_id() {
  local repo="$1" rid="$2"
  say "  ${DIM}watching run ${rid}…${RST}"
  gh run watch "$rid" --repo "$repo" --exit-status >/dev/null 2>&1
  local ok=$?
  say "  run ${rid}: $(gh run view "$rid" --repo "$repo" --json conclusion --jq .conclusion 2>/dev/null)"
  return $ok
}

# Watch the publish workflow run for the exact release tag just created.
watch_publish_run() {
  local repo="$1" tag="$2" tries=20 rid=
  while [ "$tries" -gt 0 ] && [ -z "$rid" ]; do
    rid="$(publish_run_for_tag "$repo" "$tag")"
    [ -n "$rid" ] && break
    sleep 3; tries=$((tries-1))
  done
  [ -z "$rid" ] && { say "  ${YLW}!${RST} could not find a triggered publish run"; return 1; }
  watch_run_id "$repo" "$rid"
}

retry_failed_publish_run() {
  local repo="$1" tag="$2" rid conclusion
  rid="$(publish_run_for_tag "$repo" "$tag")"
  [ -n "$rid" ] || { say "  ${RED}✗ no publish run exists for tag $tag${RST}"; return 1; }
  conclusion="$(gh run view "$rid" --repo "$repo" --json conclusion --jq .conclusion 2>/dev/null || true)"
  case "$conclusion" in
    success)
      say "  ${DIM}existing publish run succeeded; waiting for registry propagation${RST}"
      return 0 ;;
    failure|cancelled|timed_out|action_required)
      say "  ${BLU}rerun${RST} failed publish workflow $rid for tag $tag"
      gh run rerun "$rid" --repo "$repo" || return 1
      watch_run_id "$repo" "$rid" ;;
    *)
      say "  ${DIM}publish run $rid is still active${RST}"
      watch_run_id "$repo" "$rid" ;;
  esac
}

prepare_bump() {
  local key="$1" dir="$2" repo="$3" kind="$4" pkg="$5" manifest="$6" level="$7"
  local changed_output target file
  local changed_files=()

  assess "$key" "$dir" "$repo" "$kind" "$pkg" "$manifest"
  plan_bump "$key" "$dir" "$kind" "$level"
  if [ "$A_verdict" != BUMP ]; then
    say "  ${RED}✗ bump precondition failed: $A_detail${RST}"
    return 1
  fi
  target="$A_nextv"

  if ! changed_output="$(python3 "$ROOT/scripts/sdk_release_versions.py" update \
      "$kind" "$dir" "$manifest" "$pkg" "$target")"; then
    say "  ${RED}✗ failed to update version files${RST}"
    return 1
  fi
  while IFS= read -r file; do
    [ -n "$file" ] && changed_files+=("$file")
  done <<<"$changed_output"

  if [ "${#changed_files[@]}" -gt 0 ]; then
    git -C "$dir" add -- "${changed_files[@]}" || return 1
    git -C "$dir" diff --cached --check || {
      say "  ${RED}✗ version diff failed whitespace validation${RST}"; return 1;
    }
    say "  ${BLU}commit${RST} v$target in $repo:$A_default"
    git -C "$dir" commit -m "chore(release): bump ${key} to v${target}" || {
      say "  ${RED}✗ version commit failed${RST}"; return 1;
    }
  else
    say "  ${DIM}$kind is tag-driven; no manifest version edit required${RST}"
  fi

  say "  ${BLU}push${RST} $repo:$A_default"
  git -C "$dir" push origin "HEAD:$A_default" || {
    say "  ${RED}✗ push failed${RST}"; return 1;
  }
  A_localv="$target"
  A_nextv="$target"
  A_ahead=0
  A_verdict=RELEASE
  A_detail="prepared v$target"
}

# uses A_* from a fresh assess() of this SDK. 0 ok / 1 fail.
execute_release() {
  local key="$1" dir="$2" repo="$3" kind="$4" pkg="$5"
  local ver="$A_localv" tag; tag="$(tag_for "$kind" "$ver")"
  local notes="Automated release of ${key} SDK v${ver} via scripts/release-sdks.sh."
  local triggered=0

  # 1. push the default branch if the local checkout is ahead
  if [ "${A_ahead:-0}" -gt 0 ] && [ "$A_branch" = "$A_default" ]; then
    say "  ${BLU}push${RST} $A_ahead commit(s) to $repo:$A_default"
    git -C "$dir" push origin "HEAD:$A_default" || { say "  ${RED}✗ push failed${RST}"; return 1; }
  fi

  # 2. skip tag/release creation if the tag already exists remotely (idempotent)
  if git -C "$dir" ls-remote --tags origin "refs/tags/$tag" 2>/dev/null | grep -q .; then
    say "  ${DIM}tag $tag already on remote — skipping tag/release creation${RST}"
  else
    say "  ${BLU}release${RST} $tag on $repo (target $A_default)"
    gh release create "$tag" --repo "$repo" --target "$A_default" \
       --title "$key v$ver" --notes "$notes" >/dev/null \
       || { say "  ${RED}✗ gh release create failed${RST}"; return 1; }
    triggered=1
  fi

  # 3. drive the publish per kind, then verify the registry
  case "$kind" in
    npm|pypi|crates)
      if [ "$triggered" -eq 1 ]; then
        watch_publish_run "$repo" "$tag" || { say "  ${RED}✗ publish workflow failed${RST}"; return 1; }
      else
        retry_failed_publish_run "$repo" "$tag" || { say "  ${RED}✗ publish workflow recovery failed${RST}"; return 1; }
      fi ;;
    go)
      say "  ${DIM}go: tag pushed; module proxy serves on first fetch${RST}" ;;
    packagist)
      say "  ${DIM}packagist: auto-syncs from the pushed tag via webhook${RST}" ;;
  esac

  say "  ${DIM}verifying registry…${RST}"
  if wait_for_registry "$kind" "$pkg" "$ver"; then
    say "  ${GRN}✓ $pkg v$ver is live${RST}"; return 0
  else
    say "  ${RED}✗ $pkg v$ver did not appear on the registry in time${RST}"; return 1
  fi
}

# ── main ──────────────────────────────────────────────────────────────────────
want() { # is $1 in the FILTER (or FILTER empty)?
  [ "${#FILTER[@]}" -eq 0 ] && return 0
  local k; for k in "${FILTER[@]}"; do [ "$k" = "$1" ] && return 0; done; return 1
}

mode="DRY-RUN (no changes)"; [ "$EXECUTE" -eq 1 ] && mode="EXECUTE"
section "Reevit SDK release orchestrator — ${mode}"
say  "${DIM}root: $ROOT${RST}"

REL_ROWS=()
REL_KEYS=()
blocked=0

for entry in "${SDKS[@]}"; do
  IFS='|' read -r key dir repo kind pkg manifest <<<"$entry"
  want "$key" || continue
  assess "$key" "$dir" "$repo" "$kind" "$pkg" "$manifest"
  bump_level="$(bump_level_for "$key" || true)"
  [ -n "$bump_level" ] && plan_bump "$key" "$dir" "$kind" "$bump_level"

  local_disp="${A_localv:-–}"
  [ "$A_verdict" = BUMP ] && local_disp="${A_localv}→${A_nextv}"
  pub_disp="${A_pubv:-none}"
  case "$A_verdict" in
    RELEASE|BUMP) c="$GRN" ;; CURRENT) c="$DIM" ;; BLOCKED) c="$RED"; blocked=1 ;;
    BEHIND|UNKNOWN) c="$YLW"; blocked=1 ;; *) c="$RST" ;;
  esac
  flags=""
  [ "${A_dirty:-0}" -gt 0 ] && flags+=" ${RED}dirty:${A_dirty}${RST}"
  [ "${A_ahead:-0}" -gt 0 ] && flags+=" ${YLW}ahead:${A_ahead}${RST}"
  [ "${A_behind:-0}" -gt 0 ] && flags+=" ${RED}behind:${A_behind}${RST}"
  [ "$A_branch" != "$A_default" ] && [ -n "$A_branch" ] && flags+=" ${DIM}on:${A_branch}${RST}"

  line="$(printf '%b%-8s%b %-9s local %-9s registry %-9s %b→ %s%b%s' \
    "$BOLD" "$key" "$RST" "$kind" "$local_disp" "$pub_disp" "$c" "$A_verdict" "$RST" "$flags")"
  say "$line"
  say "         ${DIM}${A_detail}${RST}"

  case "$A_verdict" in
    RELEASE|BUMP) REL_KEYS+=("$key"); REL_ROWS+=("$entry") ;;
  esac
done

# ── summary / act ─────────────────────────────────────────────────────────────
section "Summary"
if [ "${#REL_KEYS[@]}" -eq 0 ]; then
  if [ "$blocked" -eq 1 ]; then
    say "No SDK is currently planned for release because one or more requested actions are blocked."
  else
    say "No SDK needs a release — every package is up to date with its default branch."
  fi
else
  say "Planned for release: ${BOLD}${REL_KEYS[*]}${RST}"
fi
[ "$blocked" -eq 1 ] && say "${YLW}Some SDKs are blocked or need attention (see 'dirty'/BEHIND/UNKNOWN above).${RST}"

if [ "$EXECUTE" -eq 0 ]; then
  section "Dry run — nothing changed."
  [ "${#REL_KEYS[@]}" -gt 0 ] && say "Re-run with the same arguments plus ${BOLD}--execute${RST} to apply bumps, push, release, and publish."
  [ "$blocked" -eq 1 ] && exit 1 || exit 0
fi

# EXECUTE path
[ "$blocked" -eq 1 ] && [ "${#BUMP_KEYS[@]}" -gt 0 ] && {
  section "Execution blocked."
  say "Resolve every requested bump precondition before publishing any package."
  exit 1
}
[ "${#REL_ROWS[@]}" -eq 0 ] && { section "Nothing to execute."; exit "$blocked"; }
if [ "$ASSUME_YES" -eq 0 ]; then
  printf '\n%sApply planned bumps and publish %s? Publishing is irreversible. [y/N] %s' "$BOLD" "${REL_KEYS[*]}" "$RST"
  read -r reply; case "$reply" in y|Y|yes|YES) ;; *) say "Aborted."; exit 1 ;; esac
fi

fail=0
for row in "${REL_ROWS[@]}"; do
  IFS='|' read -r key dir repo kind pkg manifest <<<"$row"
  section "Releasing ${key}"
  bump_level="$(bump_level_for "$key" || true)"
  if [ -n "$bump_level" ]; then
    if ! prepare_bump "$key" "$dir" "$repo" "$kind" "$pkg" "$manifest" "$bump_level"; then
      fail=1; say "${RED}✗ ${key} bump did not complete${RST}"; continue
    fi
  else
    assess "$key" "$dir" "$repo" "$kind" "$pkg" "$manifest"   # refresh state right before acting
    if [ "$A_verdict" != RELEASE ]; then
      say "  ${YLW}state changed since planning (now $A_verdict) — skipping${RST}"; continue
    fi
  fi
  if ! execute_release "$key" "$dir" "$repo" "$kind" "$pkg"; then
    fail=1; say "${RED}✗ ${key} release did not complete${RST}"
  fi
done

section "Done."
exit "$fail"
