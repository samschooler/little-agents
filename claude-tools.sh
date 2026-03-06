#!/usr/bin/env bash
# claude-tools: tmux session manager + quota tracker for Claude Code
# Source this file from your .bashrc or .zshrc

CLAUDE_TOOLS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-${(%):-%x}}")" && pwd)"

alias cld='claude --dangerously-skip-permissions'

_ct_pick_repo() {
    local _rkeys="qwertyuiopasdfghjlzxcvbnm"
    local _repos=() _i=0
    for _d in ~/repo/*/; do
        [ -d "$_d" ] || continue
        _repos+=("${_d%/}")
        local _k=${_rkeys:$_i:1}
        echo "    ${_k}) $(basename "${_d%/}")"
        _i=$((_i+1))
    done
    if [ ${#_repos[@]} -eq 0 ]; then
        echo "    (none)"
    fi
    echo ""
    if [ ${#_repos[@]} -gt 0 ]; then
        local _rmax=${_rkeys:$((_i-1)):1}
        echo -ne "  \033[0;90m[${_rkeys:0:1}-${_rmax}] select  [n] new  [esc] cancel:\033[0m "
    else
        echo -ne "  \033[0;90m[n] new  [esc] cancel:\033[0m "
    fi
    read -rsn1 _rn
    if [[ "$_rn" = $'\e' || -z "$_rn" ]]; then
        return 1
    elif [[ "$_rn" =~ ^[nN]$ ]]; then
        echo ""
        read -rp "  New repo name: " _newrepo
        if [ -n "$_newrepo" ]; then
            mkdir -p ~/repo/"$_newrepo"
            _ct_picked_repo=~/repo/"$_newrepo"
            return 0
        fi
        return 1
    fi
    local _ridx="${_rkeys%%"$_rn"*}"
    if [ ${#_ridx} -lt ${#_repos[@]} ]; then
        _ct_picked_repo="${_repos[${#_ridx}]}"
        return 0
    fi
    return 1
}

nt() {
    local _name="${1:?session name required}"
    _ct_picked_repo=""
    echo "  Select repo:"
    if _ct_pick_repo; then
        tmux new-session -s "$_name" -c "$_ct_picked_repo" 'claude --dangerously-skip-permissions'
    fi
}

kt() { tmux kill-session -t "${1:?session name required}"; }
at() { tmux a -t "${1:?session name required}"; }

_ct_session_status() {
    local _session=$1
    local _live=$(cat "/tmp/claude-status-${_session}" 2>/dev/null)
    case "$_live" in
        waiting|"")  echo "\033[1;36m◉ waiting\033[0m" ;;
        thinking)    echo "\033[1;33m💭 thinking\033[0m" ;;
        *)           echo "\033[1;33m⚡${_live}\033[0m" ;;
    esac
}

_ct_quota_line() {
    # Output: total_tokens input output cache_read reset_time
    local _q=($(python3 "$CLAUDE_TOOLS_DIR/claude-quota.py" 2>/dev/null))
    local _tot=${_q[0]:-0} _in=${_q[1]:-0} _out=${_q[2]:-0} _cache=${_q[3]:-0} _rst=${_q[4]:---}
    echo -e "  \033[1;36m⚡${_tot} tokens\033[0m \033[0;90m(in:${_in} out:${_out} cache:${_cache}) resets ${_rst}\033[0m"
}

cs() {
    _ct_quota_line
    local _has=false
    while IFS=' ' read -r _s _p _c _cwd; do
        if ! $_has; then echo "  Sessions:"; _has=true; fi
        local _st=""
        [ "$_c" = "claude" ] && _st=" $(_ct_session_status "$_s")"
        local _dir="${_cwd#$HOME/repo/}"
        echo -e "    $_s \033[0;90m[$_dir]\033[0m$_st"
    done < <(tmux list-panes -a -F '#{session_name} #{pane_pid} #{pane_current_command} #{pane_current_path}' 2>/dev/null | sort -u -k1,1)
    if ! $_has; then echo "  No active tmux sessions"; fi
}

cst() {
    local _keys="qwertyuiopasdfghjlzxcvbnm"
    while true; do
        clear
        _ct_quota_line
        local _sessions=()
        while IFS=' ' read -r _s _p _c _cwd; do
            _sessions+=("$_s")
            local _i=${#_sessions[@]}
            local _k=${_keys:$((_i-1)):1}
            local _st=""
            [ "$_c" = "claude" ] && _st=" $(_ct_session_status "$_s")"
            local _dir="${_cwd#$HOME/repo/}"
            echo -e "    \033[1;37m${_k})\033[0m $_s \033[0;90m[$_dir]\033[0m$_st"
        done < <(tmux list-panes -a -F '#{session_name} #{pane_pid} #{pane_current_command} #{pane_current_path}' 2>/dev/null | sort -u -k1,1)
        if [ ${#_sessions[@]} -eq 0 ]; then
            echo "  No active tmux sessions"
        fi
        local _max=${_keys:$((${#_sessions[@]}-1)):1}
        echo ""
        echo -ne "  \033[0;90m[${_keys:0:1}-${_max}] attach  [k] kill  [n] new  [esc] quit\033[0m "
        read -rsn1 -t 0.5 _n || continue
        if [[ "$_n" = $'\e' ]]; then
            return
        elif [[ "$_n" =~ ^[kK]$ ]]; then
            echo ""
            echo -ne "  \033[0;90mKill session [letter or esc]:\033[0m "
            read -rsn1 _kn
            if [[ "$_kn" = $'\e' || -z "$_kn" ]]; then
                continue
            fi
            local _kidx="${_keys%%"$_kn"*}"
            if [ ${#_kidx} -lt ${#_sessions[@]} ]; then
                tmux kill-session -t "${_sessions[${#_kidx}]}"
            fi
        elif [[ "$_n" =~ ^[nN]$ ]]; then
            echo ""
            read -rp "  Session name: " _name
            if [ -n "$_name" ]; then
                echo "  Select repo:"
                _ct_picked_repo=""
                if _ct_pick_repo; then
                    tmux new-session -d -s "$_name" -c "$_ct_picked_repo" 'claude --dangerously-skip-permissions' && tmux a -t "$_name"
                fi
            fi
        else
            local _idx="${_keys%%"$_n"*}"
            if [ ${#_idx} -lt ${#_sessions[@]} ]; then
                tmux a -t "${_sessions[${#_idx}]}"
            fi
        fi
    done
}

# Show status on SSH login
if [ -n "$SSH_CONNECTION" ]; then
    echo ""
    echo "  Shortcuts:"
    echo "    cld  - claude --dangerously-skip-permissions"
    echo "    cs   - claude session status"
    echo "    cst  - claude session manager (live, k to kill)"
    echo "    nt   - tmux new-session <name>"
    echo "    kt   - tmux kill-session <name>"
    echo "    at   - tmux attach <name>"
    echo "    C-b d - detach from session"
    echo ""
    cs
    echo ""
fi
