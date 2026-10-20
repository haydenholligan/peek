# Sent messages in Peek

Status: confirmed.

## Behavior

- Press `s` to show or hide sent messages above the current reply.
- Sent messages start hidden. The toggle applies across replies while Peek is
  open and resets to hidden when Peek reopens.
- Toggling moves to the top so newly revealed sent messages are immediately visible.
- Show all sent messages associated with the reply, in order, with separate
  “You” labels. Render submitted text as Markdown.
- Include attachment names when available, otherwise a descriptive placeholder
  such as “Image 1”. Exclude automatically supplied instructions and context.
- Opening Peek selects the latest finished reply. `]` can reach newer
  unanswered messages, shown with a waiting indicator.
- If there are no finished replies, open directly on unanswered messages.
  Unanswered messages are always visible regardless of the toggle.
- Read a snapshot when Peek opens. Reopen to see a newly finished reply.
- Preserve existing navigation, scrolling, and return-to-agent behavior.

## Implementation boundaries

Support Codex and Claude Code using their existing transcripts and session
discovery. Preserve sent-message order and use available turn identifiers to
associate messages with replies; do not assume messages strictly alternate.
Keep existing replies readable when their sent messages cannot be recovered.

Current Codex transcripts expose submitted content in completed `UserMessage`
items, with turn identifiers also present on reply completion events. Claude
user records include both human submissions and tool results, so extraction
must distinguish their origin and content. Attachment names are not available
in every format.

## Verification

- Parse representative Codex and Claude records, including multiple submissions,
  automatic context, tool results, attachments, and incomplete transcript lines.
- Confirm messages are associated with the correct reply without duplication.
- Verify `s`, navigation while messages are visible, reopening defaults,
  unanswered messages, and sessions with no finished reply.
- Run the existing Go tests, including the terminal integration test that checks
  returning to the agent and preserving an unfinished draft.

