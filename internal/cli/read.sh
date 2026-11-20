b=@BOUNDARY@
ports='@PORTS@'
nl='
'
started=$(date +%s)
bounded=
command -v timeout >/dev/null 2>&1 && bounded=1
terminals_read=0

late() {
	[ $(($(date +%s) - started)) -ge 8 ]
}

frame() {
	case $2 in
	*"$b"*)
		printf '%s begin %s\n\n%s end boundary\n' "$b" "$1" "$b"
		exit 0
		;;
	esac
	printf '%s begin %s\n%s\n%s end %s\n' "$b" "$1" "$2" "$b" "$3"
}

capture() {
	limit=$1
	shift
	captured=$({ "$@" 2>/dev/null; printf '\n%s status %s' "$b" "$?"; } | head -c "$limit")
	case $captured in
	*"$b status "*)
		status=${captured##*"$b status "}
		captured=${captured%"$nl$b status "*}
		;;
	*) status=too-large ;;
	esac
	[ "$status" = 0 ] || captured=
}

get() {
	capture 262144 curl --noproxy '*' -sS -f --max-time 2 "http://127.0.0.1:$1$2"
}

field() {
	printf '%s' "$captured" | grep -o "\"$1\" *: *\"[A-Za-z0-9._-]*\"" | head -n 1 | sed 's/.*"\([A-Za-z0-9._-]*\)"$/\1/'
}

if [ -n "$bounded" ]; then
	capture 65536 timeout -k 1 3 df -Pk / .
	frame disk "$captured" "$status"
fi

if ! command -v curl >/dev/null 2>&1; then
	frame cao "" no-curl
	ports=
fi
for port in $ports; do
	if late; then
		frame "cao $port sessions" "" late
		continue
	fi
	get "$port" /sessions
	frame "cao $port sessions" "" "$status"
	[ "$status" = 0 ] || continue
	sessions=$(printf '%s' "$captured" | grep -o '"id" *: *"cao-[A-Za-z0-9_-]*"' | sed 's/.*"\(cao-[A-Za-z0-9_-]*\)"$/\1/' | sort -u | head -n 16)
	for session in $sessions; do
		late && break
		get "$port" "/sessions/$session/terminals"
		[ "$status" = 0 ] || continue
		terminals=$(printf '%s' "$captured" | grep -o '"id" *: *"[0-9a-f]\{8\}"' | sed 's/.*"\([0-9a-f]\{8\}\)"$/\1/' | sort -u | head -n 32)
		for terminal in $terminals; do
			late && break
			[ "$terminals_read" -lt 64 ] || break
			terminals_read=$((terminals_read + 1))
			get "$port" "/terminals/$terminal"
			frame "cao $port terminal $terminal" "$captured" "$status"
			[ "$status" = 0 ] && [ -n "$bounded" ] || continue
			window=$(field name)
			tmux_session=$(field session_name)
			[ -n "$window" ] && [ -n "$tmux_session" ] || continue
			capture 16384 timeout -k 1 3 tmux capture-pane -p -J -S -40 -t "$tmux_session:$window"
			frame "tail $port $terminal" "$captured" "$status"
		done
	done
done

[ -n "$bounded" ] || frame note "" no-timeout
