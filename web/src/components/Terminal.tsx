import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { recordingURL, shellSocketURL } from "../api/client";

type ConnState = "connecting" | "open" | "closed" | "error";

const theme = {
  background: "#0b0f17",
  foreground: "#d7dce5",
  cursor: "#7dd3fc",
};

function newTerminal(): Terminal {
  return new Terminal({
    convertEol: false,
    cursorBlink: true,
    fontFamily:
      'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace',
    fontSize: 13,
    theme,
    scrollback: 5000,
  });
}

// LiveTerminal bridges an xterm instance to the device PTY over a
// WebSocket: binary frames carry terminal bytes, text frames carry
// control messages (resize out, exit/error in).
export function LiveTerminal({
  deviceId,
  onClosed,
}: {
  deviceId: string;
  onClosed?: () => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<ConnState>("connecting");
  const [note, setNote] = useState<string>("");

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;

    const term = newTerminal();
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();
    term.focus();

    const ws = new WebSocket(
      shellSocketURL(deviceId, term.cols, term.rows),
    );
    ws.binaryType = "arraybuffer";

    const enc = new TextEncoder();
    const dec = new TextDecoder();

    ws.onopen = () => setState("open");

    ws.onmessage = (ev) => {
      if (typeof ev.data === "string") {
        try {
          const msg = JSON.parse(ev.data) as { type: string; message?: string };
          if (msg.type === "exit") {
            setNote("Session ended.");
            setState("closed");
          } else if (msg.type === "error") {
            setNote(msg.message || "Session error.");
            setState("error");
          }
        } catch {
          /* ignore malformed control frame */
        }
        return;
      }
      term.write(new Uint8Array(ev.data as ArrayBuffer));
    };

    ws.onclose = () => {
      setState((s) => (s === "open" || s === "connecting" ? "closed" : s));
      onClosed?.();
    };
    ws.onerror = () => setState("error");

    const dataSub = term.onData((d) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d));
    });

    const sendResize = () => {
      if (ws.readyState !== WebSocket.OPEN) return;
      ws.send(
        JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }),
      );
    };
    const resizeSub = term.onResize(sendResize);

    const onWindowResize = () => {
      try {
        fit.fit();
      } catch {
        /* terminal not visible */
      }
    };
    window.addEventListener("resize", onWindowResize);

    void dec; // reserved for future text decoding needs

    return () => {
      window.removeEventListener("resize", onWindowResize);
      dataSub.dispose();
      resizeSub.dispose();
      ws.close();
      term.dispose();
    };
  }, [deviceId, onClosed]);

  return (
    <div className="terminal-wrap">
      <div className={`terminal-status ${state}`}>
        <span className="terminal-dot" />
        {state === "connecting" && "Connecting…"}
        {state === "open" && "Connected"}
        {state === "closed" && (note || "Disconnected")}
        {state === "error" && (note || "Connection error")}
      </div>
      <div className="terminal-host" ref={hostRef} />
    </div>
  );
}

interface CastEvent {
  t: number;
  data: string;
}

// CastPlayer replays an asciinema v2 recording into a read-only xterm by
// scheduling each output event at its recorded offset.
export function CastPlayer({ sessionId }: { sessionId: string }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<"loading" | "ready" | "error">(
    "loading",
  );
  const [error, setError] = useState("");

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    let disposed = false;
    const timers: ReturnType<typeof setTimeout>[] = [];

    const term = newTerminal();
    term.options.cursorBlink = false;
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);

    (async () => {
      try {
        const res = await fetch(recordingURL(sessionId), {
          credentials: "include",
        });
        if (!res.ok) throw new Error(`recording unavailable (${res.status})`);
        const text = await res.text();
        if (disposed) return;

        const lines = text.split("\n").filter((l) => l.trim() !== "");
        const header = JSON.parse(lines[0]) as { width?: number; height?: number };
        if (header.width && header.height) term.resize(header.width, header.height);
        else fit.fit();

        const events: CastEvent[] = [];
        for (const line of lines.slice(1)) {
          const ev = JSON.parse(line) as [number, string, string];
          if (ev[1] === "o") events.push({ t: ev[0], data: ev[2] });
        }
        setStatus("ready");

        // Compress long idle gaps so playback stays watchable.
        let clock = 0;
        let prev = 0;
        for (const ev of events) {
          clock += Math.min(ev.t - prev, 2);
          prev = ev.t;
          const at = clock;
          timers.push(
            setTimeout(() => {
              if (!disposed) term.write(ev.data);
            }, at * 1000),
          );
        }
      } catch (e) {
        if (!disposed) {
          setError(e instanceof Error ? e.message : String(e));
          setStatus("error");
        }
      }
    })();

    return () => {
      disposed = true;
      timers.forEach(clearTimeout);
      term.dispose();
    };
  }, [sessionId]);

  return (
    <div className="terminal-wrap">
      <div className="terminal-status">
        {status === "loading" && "Loading recording…"}
        {status === "ready" && "Replaying recorded session"}
        {status === "error" && `Playback error: ${error}`}
      </div>
      <div className="terminal-host" ref={hostRef} />
    </div>
  );
}
