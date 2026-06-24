import { useEffect, useRef, useState } from "react";
import {
  ApiError,
  AssistantMessage,
  AssistantToolCall,
  chatAssistant,
} from "../api/client";

// Turn extends a chat message with the tool calls the assistant made on
// that turn, so the UI can show what it did under the hood.
interface Turn extends AssistantMessage {
  toolCalls?: AssistantToolCall[];
}

const GREETING =
  "Hi — I'm the rmmagic assistant. Ask me about your devices, alerts, jobs, or scripts, and I can run safe actions on your behalf.";

export function Assistant() {
  const [open, setOpen] = useState(false);
  const [disabled, setDisabled] = useState(false);
  const [turns, setTurns] = useState<Turn[]>([
    { role: "assistant", content: GREETING },
  ]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const bodyRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (bodyRef.current) {
      bodyRef.current.scrollTop = bodyRef.current.scrollHeight;
    }
  }, [turns, busy, open]);

  async function send() {
    const text = input.trim();
    if (!text || busy) return;
    const next: Turn[] = [...turns, { role: "user", content: text }];
    setTurns(next);
    setInput("");
    setBusy(true);

    // Send only the actual conversation (drop the local greeting).
    const history: AssistantMessage[] = next
      .filter((t, i) => !(i === 0 && t.role === "assistant"))
      .map((t) => ({ role: t.role, content: t.content }));

    try {
      const res = await chatAssistant(history);
      setTurns((prev) => [
        ...prev,
        {
          role: "assistant",
          content: res.reply || "(no reply)",
          toolCalls: res.tool_calls ?? undefined,
        },
      ]);
    } catch (err) {
      if (err instanceof ApiError && err.status === 503) {
        setDisabled(true);
        setTurns((prev) => [
          ...prev,
          {
            role: "assistant",
            content:
              "The AI assistant isn't configured on this server. An administrator can enable it by setting RMM_ANTHROPIC_API_KEY.",
          },
        ]);
      } else {
        const msg = err instanceof Error ? err.message : "request failed";
        setTurns((prev) => [
          ...prev,
          { role: "assistant", content: `Sorry — ${msg}.` },
        ]);
      }
    } finally {
      setBusy(false);
    }
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void send();
    }
  }

  if (!open) {
    return (
      <button
        type="button"
        className="assistant-fab"
        onClick={() => setOpen(true)}
        title="Ask the AI assistant"
        aria-label="Open AI assistant"
      >
        ✦ Ask AI
      </button>
    );
  }

  return (
    <div className="assistant-panel" role="dialog" aria-label="AI assistant">
      <div className="assistant-header">
        <span>✦ Assistant</span>
        <button
          type="button"
          onClick={() => setOpen(false)}
          aria-label="Close assistant"
        >
          ✕
        </button>
      </div>
      <div className="assistant-body" ref={bodyRef}>
        {turns.map((t, i) => (
          <div key={i} className={`assistant-msg ${t.role}`}>
            {t.toolCalls && t.toolCalls.length > 0 && (
              <div className="assistant-tools">
                {t.toolCalls.map((c, j) => (
                  <span key={j} className="assistant-tool-chip">
                    {c.name}
                  </span>
                ))}
              </div>
            )}
            <div className="assistant-bubble">{t.content}</div>
          </div>
        ))}
        {busy && (
          <div className="assistant-msg assistant">
            <div className="assistant-bubble assistant-typing">…</div>
          </div>
        )}
      </div>
      <div className="assistant-input">
        <textarea
          rows={2}
          value={input}
          disabled={disabled || busy}
          placeholder={
            disabled ? "Assistant unavailable" : "Ask about your fleet…"
          }
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={onKeyDown}
        />
        <button
          type="button"
          onClick={() => void send()}
          disabled={disabled || busy || !input.trim()}
        >
          Send
        </button>
      </div>
    </div>
  );
}
