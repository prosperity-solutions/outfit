#!/bin/sh
# Make the git under test the first `git` on PATH for the rest of the job.
#
#   install-git.sh apple    macOS system git (Apple git), via /usr/bin
#   install-git.sh brew     Homebrew git
#   install-git.sh latest   the pinned newest release, built from source
#   install-git.sh X.Y.Z    that release, built from source (e.g. the 2.36 floor)
#
# Source builds land in $OUTFIT_GIT_PREFIX_BASE/git-<ver> (default
# ~/.cache/outfit-git), which the workflow caches by version, so a warm run only
# adds the PATH entry. Needs GITHUB_PATH (set by Actions); outside Actions it
# prints the directory to put on PATH.
set -eu

# Bump LATEST together with the pinned sha256 below when a new git ships.
LATEST=2.56.0
want=${1:?usage: install-git.sh apple|brew|latest|X.Y.Z}

add_path() {
  if [ -n "${GITHUB_PATH:-}" ]; then
    printf '%s\n' "$1" >> "$GITHUB_PATH"
  else
    # shellcheck disable=SC2016  # the literal $PATH is for the user's shell
    printf 'export PATH=%s:$PATH\n' "$1"
  fi
}

case "$want" in
  apple)
    [ "$(uname -s)" = Darwin ] || { echo "apple git only exists on macOS" >&2; exit 1; }
    # Expose only /usr/bin/git, not all of /usr/bin, so tools installed by earlier
    # steps (setup-go, brew) are not shadowed.
    dir=${RUNNER_TEMP:-${TMPDIR:-/tmp}}/outfit-apple-git
    mkdir -p "$dir"
    ln -sf /usr/bin/git "$dir/git"
    add_path "$dir"
    PATH=$dir:$PATH
    ;;
  brew)
    brew list git >/dev/null 2>&1 || brew install git
    add_path "$(brew --prefix)/bin"
    PATH=$(brew --prefix)/bin:$PATH
    ;;
  *)
    ver=$want
    [ "$want" = latest ] && ver=$LATEST
    # Pinned and checked: the tarball is built and then cached across runs. The
    # values match kernel.org's signed sha256sums.asc.
    case "$ver" in
      2.36.6) sha=3a54356dd0a6b23d7d0742952d5c86958ad8f797cbe41bd1952bc83ec429ea94 ;;
      2.56.0) sha=26c56c296b38c0695b26fa95f475f1d01704d2d38e73465ca30b0b2f5dc789d3 ;;
      *) echo "no pinned sha256 for git $ver: add it to scripts/ci/install-git.sh" >&2; exit 1 ;;
    esac
    base=${OUTFIT_GIT_PREFIX_BASE:-$HOME/.cache/outfit-git}
    prefix=$base/git-$ver
    if [ ! -x "$prefix/bin/git" ]; then
      work=$(mktemp -d)
      trap 'rm -rf "$work"' EXIT
      curl -fsSL --retry 3 --retry-delay 2 \
        "https://mirrors.edge.kernel.org/pub/software/scm/git/git-$ver.tar.xz" -o "$work/git.tar.xz"
      if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$work/git.tar.xz" | cut -d' ' -f1)
      else got=$(shasum -a 256 "$work/git.tar.xz" | cut -d' ' -f1); fi
      [ "$got" = "$sha" ] || { echo "git-$ver.tar.xz sha256 mismatch: got $got, want $sha" >&2; exit 1; }
      tar -xJf "$work/git.tar.xz" -C "$work"
      jobs=$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)
      make -C "$work/git-$ver" -j"$jobs" prefix="$prefix" \
        NO_GETTEXT=1 NO_TCLTK=1 NO_PERL=1 NO_PYTHON=1 NO_EXPAT=1 NO_CURL=1 install >/dev/null
    fi
    add_path "$prefix/bin"
    PATH=$prefix/bin:$PATH
    ;;
esac

got=$(git --version)
echo "git under test: $got ($(command -v git))"
case "$want" in
  apple) case "$got" in *Apple*) ;; *) echo "expected Apple git, got: $got" >&2; exit 1 ;; esac ;;
  brew)  case "$got" in *Apple*) echo "expected Homebrew git, got: $got" >&2; exit 1 ;; esac ;;
  latest) case "$got" in "git version $LATEST"*) ;; *) echo "expected $LATEST, got: $got" >&2; exit 1 ;; esac ;;
  *)     case "$got" in "git version $want"*) ;; *) echo "expected $want, got: $got" >&2; exit 1 ;; esac ;;
esac
