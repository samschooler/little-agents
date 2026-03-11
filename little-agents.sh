#!/usr/bin/env bash
# little-agents: tmux session manager + quota tracker for Claude Code
# Source this file from your .bashrc or .zshrc

LITTLE_AGENTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-${(%):-%x}}")" && pwd)"
_CT_STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/little-agents"


# zsh uses -k1, bash uses -n1
if [ -n "$ZSH_VERSION" ]; then
    _ct_readkey() { read -rsk1 "$@"; }
else
    _ct_readkey() { read -rsn1 "$@"; }
fi

_CT_KEYS="qwertyuiopasdfghjlzxcvbnm"

_ct_launcher_get() {
    local _launcher="claude"
    if [ -f "$_CT_STATE_DIR/launcher" ]; then
        read -r _launcher < "$_CT_STATE_DIR/launcher"
    fi
    case "$_launcher" in
        claude|codex) printf '%s' "$_launcher" ;;
        *) printf 'claude' ;;
    esac
}

_ct_launcher_set() {
    mkdir -p "$_CT_STATE_DIR"
    printf '%s\n' "$1" > "$_CT_STATE_DIR/launcher"
}

_ct_launcher_cmd() {
    case "$1" in
        codex) printf 'codex --dangerously-bypass-approvals-and-sandbox' ;;
        *) printf 'claude --dangerously-skip-permissions' ;;
    esac
}

_ct_keylabel() {
    local _i=$1 _klen=${#_CT_KEYS}
    local _p=$((_i / _klen)) _l=${_CT_KEYS:$((_i % _klen)):1}
    if [ $_p -eq 0 ]; then printf '%s' "$_l"; else printf '%s%s' "$_p" "$_l"; fi
}

_ct_keyidx() {
    local _input=$1
    local _klen=${#_CT_KEYS}
    local _p=0
    local _l="$_input"
    if [ ${#_input} -eq 2 ]; then _p=${_input:0:1}; _l=${_input:1:1}; fi
    local _i
    for ((_i = 0; _i < _klen; _i++)); do
        if [ "${_CT_KEYS:_i:1}" = "$_l" ]; then
            echo $((_p * _klen + _i))
            return 0
        fi
    done
    return 1
}

_ct_array_get() {
    local _name=$1 _idx=$2
    if [ -n "$ZSH_VERSION" ]; then
        _idx=$((_idx + 1))
    fi
    eval "printf '%s' \"\${${_name}[$_idx]}\""
}

# Read a selection key - if digit, wait for a letter to form prefix (e.g. "1q")
_ct_sel=""
_ct_readsel() {
    _ct_readkey "$@" _ct_sel || return 1
    if [[ "$_ct_sel" =~ ^[0-9]$ ]]; then
        local _ch2; _ct_readkey _ch2; _ct_sel="${_ct_sel}${_ch2}"
    fi
}

_ct_pick_repo() {
    local _repos=() _i=0
    for _d in ~/repo/*/; do
        [ -d "$_d" ] || continue
        _repos+=("${_d%/}")
        echo "    $(_ct_keylabel $_i)) $(basename "${_d%/}")"
        _i=$((_i+1))
    done
    if [ ${#_repos[@]} -eq 0 ]; then
        echo "    (none)"
    fi
    echo ""
    if [ ${#_repos[@]} -gt 0 ]; then
        echo -ne "  \033[0;90m[$(_ct_keylabel 0)-$(_ct_keylabel $((_i-1)))] select  [n] new  [esc] cancel:\033[0m "
    else
        echo -ne "  \033[0;90m[n] new  [esc] cancel:\033[0m "
    fi
    _ct_readsel
    if [[ "$_ct_sel" = $'\e' || -z "$_ct_sel" ]]; then
        return 1
    elif [[ "$_ct_sel" =~ ^[nN]$ ]]; then
        echo ""
        echo -n "  New repo name: "; read -r _newrepo
        if [ -n "$_newrepo" ]; then
            mkdir -p ~/repo/"$_newrepo"
            _ct_picked_repo=~/repo/"$_newrepo"
            return 0
        fi
        return 1
    fi
    local _ridx=$(_ct_keyidx "$_ct_sel")
    if [ -n "$_ridx" ] && [ "$_ridx" -lt ${#_repos[@]} ]; then
        _ct_picked_repo="$(_ct_array_get _repos "$_ridx")"
        return 0
    fi
    return 1
}

_ct_session_status() {
    local _session=$1
    local _live=$(cat "/tmp/claude-status-${_session}" 2>/dev/null)
    case "$_live" in
        waiting|"")  echo "\033[1;36m◉ waiting\033[0m" ;;
        thinking)    echo "\033[1;33m💭 thinking\033[0m" ;;
        *)           echo "\033[1;33m⚡${_live}\033[0m" ;;
    esac
}

lila() {
    local _first=true _i=0
    declare -A _prev_statuses
    tput civis 2>/dev/null  # hide cursor
    trap 'tput cnorm 2>/dev/null' INT TERM
    while true; do
        local _buf=""
        local _launcher=$(_ct_launcher_get)
        local _q=($(python3 "$LITTLE_AGENTS_DIR/little-agents-quota.py" 2>/dev/null))
        local _tot=${_q[0]:-0} _pct=${_q[1]:-0} _rst=${_q[2]:---}
        local _qc="\033[1;32m"
        [ "$_pct" -ge 50 ] 2>/dev/null && _qc="\033[1;33m"
        [ "$_pct" -ge 80 ] 2>/dev/null && _qc="\033[1;31m"
        local _sessions=()
        _i=0
        while IFS=' ' read -r _s _p _c _cwd; do
            _sessions+=("$_s")
            local _st="" _dot=""
            if [ "$_c" = "claude" ]; then
                local _cur_status=$(cat "/tmp/claude-status-${_s}" 2>/dev/null)
                local _prev=${_prev_statuses[$_s]:-}
                if [[ -n "$_prev" && "$_prev" != "waiting" && "$_prev" != "" && ( "$_cur_status" = "waiting" || -z "$_cur_status" ) ]]; then
                    if ! tmux list-clients -t "$_s" 2>/dev/null | grep -q .; then
                        touch "/tmp/claude-unread-${_s}"
                    fi
                fi
                _prev_statuses[$_s]="$_cur_status"
                [ -f "/tmp/claude-unread-${_s}" ] && _dot=" \033[1;31m●\033[0m"
                _st=" $(_ct_session_status "$_s")"
            fi
            local _dir="${_cwd#$HOME/repo/}"
            _buf+="    \033[1;37m$(_ct_keylabel $_i))\033[0m${_dot} $_s \033[0;90m[$_dir]\033[0m$_st\033[K\n"
            _i=$((_i+1))
        done < <(tmux list-panes -a -F '#{session_name} #{pane_pid} #{pane_current_command} #{pane_current_path}' 2>/dev/null | sort -u -k1,1)
        if [ ${#_sessions[@]} -eq 0 ]; then
            _buf+="  No active tmux sessions\033[K\n"
        fi
        _buf+="\033[K\n"
        _buf+="  ${_qc}⚡${_tot} (${_pct}%)\033[0m \033[0;90mresets ${_rst}\033[0m\033[K\n"
        if [ ${#_sessions[@]} -gt 0 ]; then
            _buf+="  \033[0;90m[$(_ct_keylabel 0)-$(_ct_keylabel $((${#_sessions[@]}-1)))] attach  [k] kill  [n] new  [c] cli:${_launcher}  [esc] quit\033[0m\033[K"
        else
            _buf+="  \033[0;90m[n] new  [c] cli:${_launcher}  [esc] quit\033[0m\033[K"
        fi
        if $_first; then
            clear
            _first=false
        else
            printf '\033[H'
        fi
        printf '%b' "$_buf"
        printf '\033[J'
        _ct_readsel -t 0.5 || continue
        if [[ "$_ct_sel" = $'\e' ]]; then
            tput cnorm 2>/dev/null
            return
        elif [[ "$_ct_sel" =~ ^[kK]$ ]]; then
            echo ""
            echo -ne "  \033[0;90mKill session [key or esc]:\033[0m "
            _ct_readsel
            if [[ "$_ct_sel" = $'\e' || -z "$_ct_sel" ]]; then
                continue
            fi
            local _kidx=$(_ct_keyidx "$_ct_sel")
            if [ -n "$_kidx" ] && [ "$_kidx" -lt ${#_sessions[@]} ]; then
                local _ksession=$(_ct_array_get _sessions "$_kidx")
                rm -f "/tmp/claude-unread-${_ksession}"
                tmux kill-session -t "$_ksession"
            fi
        elif [[ "$_ct_sel" =~ ^[nN]$ ]]; then
            echo ""
            echo -n "  Session name: "; read -r _name
            if [ -n "$_name" ]; then
                echo "  Select repo:"
                _ct_picked_repo=""
                if _ct_pick_repo; then
                    tmux new-session -d -s "$_name" -c "$_ct_picked_repo" "$(_ct_launcher_cmd "$(_ct_launcher_get)")" && tmux a -t "$_name"
                fi
            fi
        elif [[ "$_ct_sel" =~ ^[cC]$ ]]; then
            if [ "$_launcher" = "claude" ]; then
                _ct_launcher_set "codex"
            else
                _ct_launcher_set "claude"
            fi
        else
            local _idx=$(_ct_keyidx "$_ct_sel")
            if [ -n "$_idx" ] && [ "$_idx" -lt ${#_sessions[@]} ]; then
                local _session=$(_ct_array_get _sessions "$_idx")
                rm -f "/tmp/claude-unread-${_session}"
                tmux a -t "$_session"
            fi
        fi
    done
}

# Show status on SSH login
if [ -n "$SSH_CONNECTION" ]; then
    echo ""
    echo "  Shortcuts:"
    echo "    lila  - little agents session manager"
    echo "    C-b d - detach from session"
    echo ""
    lila
    echo ""
fi
