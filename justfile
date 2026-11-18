# sockat-go justfile
#
# version の正本は VERSION で、push 時に check-version-bumped が「試験以外の
# .go / go.mod / go.sum を変えたのに VERSION が据え置き」を止める。main に
# VERSION が push されると、release workflow が CI を通してから tag と
# GitHub Release を作る。
# README は ja が正本、en が翻訳先。push 時に check-outdated-translations が
# 「en が ja より古い」を止める。
# VCS 操作は bump-semver vcs に委譲して jj / git を透過にする。

set shell := ["bash", "-euo", "pipefail", "-c"]

set script-interpreter := ["bash", "-euo", "pipefail"]

set positional-arguments

# default: list
default: list

# show recipes
list:
    @just --list --unsorted

# ---------- lint / test ----------

# gofmt / go vet / go.mod の tidy / 他 OS の vet
check: lint-fmt lint-cross
    go vet ./...
    go mod tidy -diff

# gofmt (書き換える)
fmt:
    gofmt -w .

# gofmt で整形済みか (書き換えない。差分があれば fail)
[private]
[script]
lint-fmt:
    unformatted=$(gofmt -l .)
    if [ -n "$unformatted" ]; then
      echo "ERROR: gofmt されていない: $unformatted (just fmt)" >&2
      exit 1
    fi

# 手元で回せない OS の compile と vet (経路は OS ごとに build tag で分かれる)
[private]
[script]
lint-cross:
    for os in linux freebsd netbsd openbsd darwin; do
      CGO_ENABLED=0 GOOS=$os GOARCH=amd64 go vet ./...
    done
    CGO_ENABLED=0 GOOS=illumos GOARCH=amd64 go vet ./...
    CGO_ENABLED=0 GOOS=android GOARCH=arm64 go vet ./...

# 試験 (race detector 付き)
test *ARGS: check
    go test -race -count=1 ./... "$@"

# Linux の試験を docker で回す (macOS の手元から Linux の経路を確かめる)
#
# 既定の seccomp では filter が掛かっているので unshare(2) を呼ばず、スレッド
# cwd の経路は使えない。seccomp=unconfined でも回して、その経路が通ることも
# 確かめる。
[doc("Linux の試験を docker (golang:1) で回す。既定の seccomp と unconfined の両方")]
[script]
test-linux *ARGS:
    for sec in builtin unconfined; do
      opt=()
      if [ "$sec" = unconfined ]; then opt=(--security-opt seccomp=unconfined); fi
      echo "== seccomp: $sec"
      docker run --rm "${opt[@]}" -v "$PWD":/work -w /work \
        -v sockat-go-mod:/go/pkg/mod -v sockat-go-cache:/root/.cache/go-build \
        golang:1 go test -count=1 ./... "$@"
    done

# check + test (CI entry point)
ci: check test

# ---------- gates (push の内部) ----------

# uncommitted change がない状態か確認
[private]
ensure-clean:
    bump-semver vcs is clean

# 現在の bookmark/branch が default (= main) 上にあるか確認
[private]
[script]
check-on-default-branch:
    if ! bump-semver vcs is on-default-branch; then
        bn=$(bump-semver vcs get default-branch)
        printf >&2 "default branch (%s) に合流してから push してください\n  1. just sync         # %s@origin に rebase\n  2. just promote      # %s bookmark を current commit に forward\n" "$bn" "$bn" "$bn"
        exit 1
    fi

# 翻訳ペアの鮮度 (正本 = *-ja.md、翻訳先 = 同じ場所の *.md)
[private]
check-outdated-translations: ensure-clean
    bump-semver vcs outdated 'glob:**/*-ja.md' '$1/$2.md'

# 試験以外の .go / go.mod / go.sum が main@origin から変わっているのに VERSION が
# 上がっていなければ fail
#
# 対象を module の中身に絞るのは、利用者に届かない変更 (試験 / docs / justfile /
# CI) で version bump を強制しないため。
#
# remote にまだ main が無い (= 初回の push) 時は比べる相手が無いので通す。
# git ls-remote --exit-code は ref が無いと 2 を返す。
[private]
[script]
check-version-bumped:
    lr=0
    git ls-remote --exit-code origin refs/heads/main >/dev/null || lr=$?
    if [ "$lr" = 2 ]; then
      echo "origin に main がまだ無い (初回の push) ので version の検査を飛ばす"
      exit 0
    fi
    rc=0
    bump-semver vcs diff -q --excludes 'glob:*_test.go' main@origin -- 'glob:*.go' go.mod go.sum || rc=$?
    case "$rc" in
      0) exit 0 ;;
      1) ;;
      *) echo "ERROR: bump-semver vcs diff failed (rc=$rc). main@origin が track されていない可能性。先に 'jj git fetch' / 'git fetch' を試してください" >&2; exit 1 ;;
    esac
    bump-semver compare gt VERSION vcs:main@origin:VERSION --no-hint && exit 0
    echo 'ERROR: module の中身が変わっているが VERSION 未 bump。"just bump-version" を実行してください' >&2
    exit 1

# ---------- release flow ----------

# VERSION を上げて (既定 patch)、Release commit を作る
[doc("version を上げて Release commit を作る")]
[script]
bump-version level="patch": ensure-clean
    new_version=$(bump-semver "$1" VERSION --write --no-hint)
    bump-semver vcs commit -m "Release v${new_version}" VERSION
    echo "Version: -> ${new_version}"

# 現在の version を表示
version:
    @bump-semver get VERSION --no-hint

# 現在の worktree を default branch (= origin/<default>) に rebase
sync:
    bump-semver vcs sync --onto $(bump-semver vcs get default-branch)@origin

# default branch を現在の commit に forward (push しない)
promote:
    bump-semver vcs promote

# push (gate を全部通してから)
push: ci check-on-default-branch ensure-clean check-outdated-translations check-version-bumped
    bump-semver vcs push --branch main --jj-bookmark-auto-advance
    ccmsg notify --self --text "Monitor で 'just watch' を起動して" 2>/dev/null || true

# push した commit の workflow (CI と Release) を SHA-pinned で watch する
#
# watch-workflow.sh は gh-monitor plugin の中にあり PATH に乗らないので、
# cache から最新版を探して使う。
[doc("push した commit の workflow (CI と Release) の完了を SHA-pinned watch する")]
[script]
watch:
    ws=$(ls "$HOME"/.claude-personal/plugins/cache/gh-monitor/gh-monitor/*/scripts/watch-workflow.sh 2>/dev/null | sort -V | tail -1)
    if [ -z "$ws" ]; then
        echo "watch-workflow.sh が見つかりません (gh-monitor plugin が未インストール?)" >&2
        exit 1
    fi
    "$ws" --sha "$(bump-semver vcs get commit-id --rev "$(bump-semver vcs get default-branch)")" kawaz/sockat-go
