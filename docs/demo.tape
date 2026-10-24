# Records docs/demo.gif with VHS (https://github.com/charmbracelet/vhs).
# Run `vhs docs/demo.tape` from an empty folder, with peek and claude on PATH.
# The reader follows macOS appearance; this theme matches its Light background.
Output demo.gif
Set Shell zsh
Set FontSize 16
Set Width 1200
Set Height 720
Set Padding 24
Set TypingSpeed 35ms
Set Theme { "name": "peek-light", "background": "#fbfbfa", "foreground": "#1f2328", "cursor": "#dc8a78", "selection": "#ccd0da", "black": "#5c5f77", "red": "#d20f39", "green": "#40a02b", "yellow": "#df8e1d", "blue": "#1e66f5", "magenta": "#8839ef", "cyan": "#179299", "white": "#acb0be", "brightBlack": "#6c6f85", "brightRed": "#d20f39", "brightGreen": "#40a02b", "brightYellow": "#df8e1d", "brightBlue": "#1e66f5", "brightMagenta": "#8839ef", "brightCyan": "#179299", "brightWhite": "#bcc0cc" }

Type "peek claude"
Sleep 400ms
Enter
Sleep 3s
Type "Compare 3 ways to retry HTTP requests in Go: a short table, then one code example. No tools."
Sleep 500ms
Enter
Sleep 3s

# Skip the wait for the reply.
Hide
Wait+Screen@180s /done \d+:\d+/
Show
Sleep 2s

Type "now write table tests for "
Sleep 1.5s
Ctrl+\
Sleep 3s
Space
Sleep 2s
Space
Sleep 2.5s
Type "g"
Sleep 600ms
Type "s"
Sleep 3s
Escape
Sleep 3.5s
