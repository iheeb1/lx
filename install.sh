#!/bin/sh
set -eu

releases=https://github.com/iheeb1/lx/releases

die() {
	printf 'lx install: %s\n' "$*" >&2
	exit 1
}

have() { command -v "$1" >/dev/null 2>&1; }

fetch() {
	case $1 in
	file://*) cp "${1#file://}" "$2" || die "cannot read $1" ;;
	*)
		if have curl; then
			curl --proto '=https' --tlsv1.2 -fsSL -o "$2" "$1" || die "download failed: $1"
		elif have wget; then
			wget -q -O "$2" "$1" || die "download failed: $1"
		else
			die "need curl or wget to download lx"
		fi
		;;
	esac
}

sha256() {
	if have sha256sum; then
		sha256sum <"$1" | cut -d ' ' -f 1
	elif have shasum; then
		shasum -a 256 <"$1" | cut -d ' ' -f 1
	elif have openssl; then
		openssl dgst -sha256 -r <"$1" | cut -d ' ' -f 1
	else
		die "need sha256sum, shasum or openssl to verify the download"
	fi
}

main() {
	case $(uname -s) in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) die "no install script for $(uname -s): download the zip from $releases (Windows), or: go install github.com/iheeb1/lx/cmd/lx@latest" ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) die "no prebuilt lx for $(uname -m): go install github.com/iheeb1/lx/cmd/lx@latest" ;;
	esac
	if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
		arch=arm64
	fi

	if [ -n "${LX_BASE_URL:-}" ]; then
		base=${LX_BASE_URL%/}
	elif [ -n "${LX_VERSION:-}" ]; then
		case $LX_VERSION in
		v*) ;;
		*) LX_VERSION=v$LX_VERSION ;;
		esac
		case $LX_VERSION in
		*[!A-Za-z0-9._+-]*) die "bad LX_VERSION: $LX_VERSION" ;;
		esac
		base=$releases/download/$LX_VERSION
	else
		base=$releases/latest/download
	fi
	case $base in
	https://* | file://*) ;;
	*) die "LX_BASE_URL must start with https:// or file://" ;;
	esac
	[ -n "${LX_INSTALL_DIR:-}${HOME:-}" ] || die "set LX_INSTALL_DIR (or HOME) to say where lx goes"
	dir=${LX_INSTALL_DIR:-$HOME/.local/bin}
	case $dir in /) ;; /*) dir=${dir%/} ;; *) dir=./${dir%/} ;; esac # ./: cd must not use CDPATH or $OLDPWD
	archive=lx_${os}_${arch}.tar.gz

	tmp=$(mktemp -d "${TMPDIR:-/tmp}/lx-install.XXXXXX") || die "mktemp failed"
	new=
	trap 'rm -rf "$tmp" || :; [ -z "$new" ] || rm -f "$new" || :' EXIT
	trap 'exit 1' HUP INT TERM

	fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS"
	fetch "$base/$archive" "$tmp/$archive"
	want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/SHA256SUMS")
	case $want in *[!0-9a-f]*) want= ;; esac
	[ ${#want} -eq 64 ] || die "$archive has no valid entry in SHA256SUMS; nothing installed"
	got=$(sha256 "$tmp/$archive")
	[ "$got" = "$want" ] || die "checksum mismatch for $archive (got ${got:-nothing}, want $want); nothing installed"

	mkdir "$tmp/x"
	tar -xzf "$tmp/$archive" -C "$tmp/x" lx || die "cannot extract lx from $archive"
	[ -f "$tmp/x/lx" ] || die "$archive has no lx binary"

	mkdir -p "$dir" || die "cannot create $dir; set LX_INSTALL_DIR to a directory you can write"
	case $dir in /*) ;; *) abs=$(cd "$dir" && pwd) || die "cannot enter $dir"; dir=$abs ;; esac
	[ ! -d "$dir/lx" ] || die "$dir/lx is a directory; nothing installed"
	new=$dir/.lx.new.$$
	install -m 0755 "$tmp/x/lx" "$new" || die "cannot write to $dir; set LX_INSTALL_DIR to a directory you can write"
	out=$("$new" version </dev/null 2>&1) || die "the downloaded lx does not run on this machine: $out"
	case $out in "lx "*) ;; *) die "the downloaded lx printed an unexpected version line: $out" ;; esac
	mv -f "$new" "$dir/lx" || die "cannot move lx into $dir"
	new=

	v=${out#lx }
	v=${v%% *}
	printf 'lx %s installed to %s\n' "$v" "$dir/lx"
	case ":${PATH:-}:" in
	*":$dir:"*)
		found=$(command -v lx 2>/dev/null || true)
		if [ -n "$found" ] && [ "$found" != "$dir/lx" ]; then
			printf 'note:  %s comes first on your PATH; remove it or put %s ahead of it\n' "$found" "$dir"
		fi
		;;
	*)
		pdir=$dir
		if [ -n "${HOME:-}" ]; then
			case $dir in "$HOME"/*) pdir=\$HOME${dir#"$HOME"} ;; esac
		fi
		line="export PATH=\"$pdir:\$PATH\""
		case ${SHELL:-} in
		*/zsh) rc=.zshrc ;;
		*/bash) if [ "$os" = darwin ]; then rc=.bash_profile; else rc=.bashrc; fi ;;
		*/fish) rc=.config/fish/config.fish line="fish_add_path \"$pdir\"" ;;
		*) rc=.profile ;;
		esac
		printf 'note:  %s is not on your PATH; add this line to ~/%s:\n         %s\n' "$dir" "$rc" "$line"
		;;
	esac
	printf '%s\n' \
		'next:  lx discover   # what lx would have saved on your recent Claude Code sessions' \
		'       lx init       # install the Claude Code hook'
}

main "$@"
