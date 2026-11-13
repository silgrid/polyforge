"use client";

import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent } from "react";
import { useI18n } from "@/hooks/useI18n";
import { useEnterSendMode } from "@/hooks/useEnterSendMode";
import { copyText } from "@/lib/clipboard";
import type { PolyforgeBlocked, PolyforgePendingQuestion } from "@/lib/polyforge-status";
import {
  cannotStartCommands,
  parseOwnerMessageAck,
  type PolyforgeConversationState,
  type PolyforgeOwnerCommand,
  type PolyforgeOwnerEcho,
} from "@/lib/polyforge-conversation-pure";
import { cancelCommand, KNOWN_BLOCKED_KINDS, resumeCommand } from "@/lib/polyforge-decisions-pure";
import { ComposerBox } from "./ComposerBox";
import { MAX_MARKDOWN_CHARS, SafeMarkdownBody } from "./SafeMarkdownBody";

const AMBER = "#d97706";
// The question's text: Markdown at the progress flow's size (12px, line
// height 1.7), so three lines are 5.1em.
const QUESTION_TEXT_STYLE = { minWidth: 0, color: "var(--text)", fontSize: 12, lineHeight: 1.7, "--chat-font-size-offset": "-2px" } as CSSProperties;

/** A copyable owner command (`pf resume <wi>` / `pf cancel <wi>`): the
 *  command as text and a copy button. */
function CopyableCommand({ command, name }: { command: string; name: PolyforgeOwnerCommand }) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  return (
    <div data-pf-chat-command={name} style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap", fontSize: 11 }}>
      <code style={{ fontFamily: "var(--font-mono, monospace)", color: "var(--text)", background: "var(--bg-subtle)", padding: "1px 4px", borderRadius: 3, overflowWrap: "anywhere" }}>{command}</code>
      <button
        type="button"
        data-pf-chat-command-copy={name}
        onClick={() => void copyText(command).then(() => setCopied(true), () => setCopied(false))}
        style={{ padding: "0 6px", fontSize: 11, border: "1px solid var(--border)", borderRadius: 4, background: "none", color: "var(--text-muted)", cursor: "pointer" }}
      >
        {copied ? t("polyforge.decisions.copied") : t("polyforge.decisions.copy")}
      </button>
    </div>
  );
}

/** The owner commands a park no message wakes leaves (design #483 §7). */
function CopyableCommands({ wi, profile, commands }: { wi: string; profile?: string; commands: readonly PolyforgeOwnerCommand[] }) {
  if (commands.length === 0) return null;
  return (
    <div data-pf-chat-commands="" style={{ display: "flex", flexDirection: "column", gap: 4, minWidth: 0 }}>
      {commands.map((name) => <CopyableCommand key={name} name={name} command={name === "resume" ? resumeCommand(wi, profile) : cancelCommand(wi, profile)} />)}
    </div>
  );
}

/** A park's cause (the watch snapshot's `blocked`,/#499): the
 *  kind labelled, the step, the reason verbatim and the required action. */
function ParkCause({ blocked }: { blocked: PolyforgeBlocked }) {
  const { t } = useI18n();
  const kind = KNOWN_BLOCKED_KINDS.includes(blocked.kind) ? t(`polyforge.blocked.kind.${blocked.kind}`) : blocked.kind;
  return (
    <div data-pf-chat-park-cause={blocked.kind} style={{ display: "flex", flexDirection: "column", gap: 2, padding: "6px 8px", borderLeft: `3px solid ${AMBER}`, borderRadius: 4, background: "rgba(217, 119, 6, 0.10)", minWidth: 0, fontSize: 11, lineHeight: "16px" }}>
      <div style={{ color: AMBER, fontWeight: 600, overflowWrap: "anywhere" }}>
        {t("polyforge.blocked.title", { kind })}
        {blocked.step && <>{" · "}<span data-pf-chat-park-step="">{blocked.step}</span></>}
      </div>
      <div data-pf-chat-park-reason="" style={{ color: "var(--text)", whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{blocked.reason}</div>
      {blocked.requiredAction && (
        <div data-pf-chat-park-action="" style={{ color: "var(--text-muted)", whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{t("polyforge.blocked.action", { action: blocked.requiredAction })}</div>
      )}
    </div>
  );
}

/**
 * The job card's bottom (workflow contract v2 §5.4, change 14; workflow
 * contract 4, design #483 §7,) — the same interaction form as
 * the main session: the question the step waits on (highlighted, its
 * Markdown rendered — — the verbatim text on hover),
 * and ONE input box — the main session composer's (`ComposerBox`,
 * the send button inside its border) — whose words go to the
 * work item through `POST /api/polyforge/answer` (`pf answer <wi> --text`,
 * no question id): to the step's own agent while it runs AND while it waits
 * on its `pf step ask` (a reply and a live interjection are one path, design
 * §5.2 (3)–(4)), and — the work item parked (blocked or paused) — as the
 * message that wakes the step the park belongs to (a short hint says so,
 * with the copyable `pf resume <wi>` that wakes it without words,
 *).
 * Enter sends (the main composer's send-key setting, Shift+Enter a new
 * line); a rejection keeps the words in the box with pf's refusal beside
 * it; an accepted message is echoed into the flow at once (`onSent`) with
 * how far its delivery got.
 *
 * Only a park whose agent cannot start closes the box (§7): the reason and
 * the copyable `pf resume` / `pf cancel` commands instead — read from the
 * watch snapshot where it proves it (`conversationState`), or from the
 * daemon's cannot-start refusal of a message, shown under the box with the
 * words kept in it (`pf cancel` alone when the park's lane snapshot targets
 * another contract: WAKE_CANNOT_START_CONTRACT). A state that is no park (not dispatched yet, ended,
 * the daemon offline) shows the reason alone.
 *
 * A park shows its cause above all this: the watch
 * snapshot's `blocked` — kind, step, reason, required action — as the panel
 * passes it; a step that runs or waits shows none, even with a stale one.
 *
 * The card has no buttons: to continue, send back, let a
 * gate through or cancel, the owner tells the step in the box, and it hands
 * off accordingly; the copyable commands are the way only where no agent
 * can start.
 */
export function PolyforgeJobComposer({
  wi,
  profile,
  state,
  question,
  blocked,
  onSent,
}: {
  /** The work item's id (the routes need it); undefined while unknown. */
  wi: string | undefined;
  profile?: string;
  state: PolyforgeConversationState;
  /** The question the step waits on (the watch snapshot's), if any. */
  question: PolyforgePendingQuestion | null;
  /** The park's cause (the watch snapshot's `blocked`), if any: shown only
   *  while the state is a park. */
  blocked?: PolyforgeBlocked;
  /** An accepted message, for the flow's echo. */
  onSent: (echo: PolyforgeOwnerEcho) => void;
}) {
  const { t } = useI18n();
  const enterSendMode = useEnterSendMode();
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // the daemon's cannot-start refusal of the last message: its words and
  // the commands left (`pf cancel` alone for another contract's snapshot)
  const [cannotStart, setCannotStart] = useState<{ text: string; commands: readonly PolyforgeOwnerCommand[] } | null>(null);
  const [questionOpen, setQuestionOpen] = useState(false);
  const boxRef = useRef<HTMLTextAreaElement | null>(null);

  // Another work item is another conversation: an unsent draft and a
  // refusal belong to the one they were typed for.
  useEffect(() => {
    setText("");
    setError(null);
    setCannotStart(null);
    setQuestionOpen(false);
  }, [wi]);
  // A refusal answers the park it was sent to: once the work item leaves
  // it (resumed, cancelled, running again) the refusal is history.
  const parkKey = state.input === "open" ? (state.park ?? (state.awaiting ? "awaiting" : "running")) : state.reason;
  useEffect(() => {
    setCannotStart(null);
  }, [parkKey]);

  const open = state.input === "open";
  const canSend = open && Boolean(wi) && !sending && text.trim() !== "";
  const send = async () => {
    if (!canSend || !wi) return;
    setSending(true);
    setError(null);
    setCannotStart(null);
    const words = text;
    try {
      const response = await fetch("/api/polyforge/answer", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ wi, ...(profile && profile !== "default" ? { profile } : {}), text: words }),
      });
      const body = await response.json().catch(() => ({})) as { ok?: boolean; ack?: unknown; error?: string; reason?: string };
      if (body.ok !== true) {
        // the words stay in the box either way
        const commands = cannotStartCommands(body.reason);
        if (commands) setCannotStart({ text: body.error || body.reason || "", commands });
        else setError(body.error || `HTTP ${response.status}`);
        return;
      }
      const ack = parseOwnerMessageAck(body.ack) ?? { kind: "message" as const, delivered: false };
      onSent({ at: ack.at ?? new Date().toISOString(), text: words, ack });
      setText("");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setSending(false);
      boxRef.current?.focus();
    }
  };
  const onKey = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key !== "Enter" || event.nativeEvent.isComposing) return;
    const sends = enterSendMode === "ctrlEnter" ? (event.ctrlKey || event.metaKey) : !event.shiftKey && !event.ctrlKey && !event.metaKey && !event.altKey;
    if (!sends) return;
    event.preventDefault();
    void send();
  };

  // a park (wakeable, or one whose agent cannot start) shows its cause
  const parked = state.input === "open" ? Boolean(state.park) : state.reason === "contract" || state.reason === "candidates";
  const label = state.input !== "open" ? ""
    : t(state.park ? "polyforge.chat.placeholderParked" : state.awaiting ? "polyforge.chat.placeholderAwaiting" : "polyforge.chat.placeholderRunning");
  return (
    <section
      data-pf-chat=""
      data-pf-chat-state={state.input === "open" ? (state.park ? "parked" : state.awaiting ? "awaiting" : "running") : state.reason}
      {...(state.input === "open" && state.park ? { "data-pf-chat-park": state.park } : {})}
      aria-label={t("polyforge.chat.title")}
      style={{ flexShrink: 0, display: "flex", flexDirection: "column", gap: 4, padding: "6px 8px 8px", borderTop: `1px solid ${open && (state.awaiting || state.park) ? AMBER : "var(--border)"}`, background: "var(--bg-panel)", minWidth: 0, fontSize: 12 }}
    >
      {parked && blocked && <ParkCause blocked={blocked} />}
      {open && state.awaiting && question && (
        // The question the owner answers, highlighted (design §5.4), its
        // Markdown rendered (the step-ask question table,; over
        // MAX_MARKDOWN_CHARS plain wrapped text, like the progress flow),
        // folded to three lines' height until opened.
        <div data-pf-chat-question="" data-pf-chat-question-id={question.id} style={{ display: "flex", flexDirection: "column", gap: 2, padding: "6px 8px", borderLeft: `3px solid ${AMBER}`, borderRadius: 4, background: "rgba(217, 119, 6, 0.10)", minWidth: 0 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 6, color: AMBER, fontWeight: 600, fontSize: 11 }}>
            <span aria-hidden="true">?</span>
            <span>{t("polyforge.chat.question")}{question.step ? ` · ${question.step}` : ""}</span>
            <button
              type="button"
              data-pf-chat-question-toggle=""
              aria-expanded={questionOpen}
              onClick={() => setQuestionOpen((value) => !value)}
              style={{ marginLeft: "auto", padding: 0, background: "none", border: "none", color: "var(--accent)", cursor: "pointer", fontSize: 11 }}
            >
              {t(questionOpen ? "polyforge.goal.collapse" : "polyforge.goal.expand")}
            </button>
          </div>
          <div
            data-pf-chat-question-text=""
            title={question.question}
            style={questionOpen ? QUESTION_TEXT_STYLE : { ...QUESTION_TEXT_STYLE, maxHeight: "5.1em", overflow: "hidden" }}
          >
            {question.question.length > MAX_MARKDOWN_CHARS
              ? <div style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{question.question}</div>
              : <SafeMarkdownBody>{question.question}</SafeMarkdownBody>}
          </div>
        </div>
      )}
      {open && state.park && (
        // Parked (design #483 §7): the message wakes the step.
        <div data-pf-chat-parked={state.park} style={{ color: AMBER, fontSize: 11, overflowWrap: "anywhere" }}>{t(`polyforge.chat.parked.${state.park}`)}</div>
      )}
      {open && state.park && wi && (
        // the command that wakes the park without words
        <CopyableCommands wi={wi} profile={profile} commands={["resume"]} />
      )}
      {open ? (
        <>
          <ComposerBox
            textareaRef={boxRef}
            inputData={{ "data-pf-chat-input": "" }}
            value={text}
            disabled={!wi || sending}
            placeholder={label}
            onChange={setText}
            onKeyDown={onKey}
            borderColor={state.awaiting || state.park ? AMBER : undefined}
            send={{
              label: sending ? t("polyforge.chat.sending") : t("polyforge.chat.send"),
              active: canSend,
              disabled: !canSend,
              onClick: () => void send(),
              data: { "data-pf-chat-send": "" },
            }}
          />
          {error && <div data-pf-chat-error="" style={{ color: "#dc2626", fontSize: 11, whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{t("polyforge.chat.failed", { error })}</div>}
          {cannotStart && wi && (
            // the daemon's cannot-start refusal: nothing was recorded; why, and the moves left
            <div data-pf-chat-cannot-start="" style={{ display: "flex", flexDirection: "column", gap: 4, padding: "6px 8px", border: `1px solid ${AMBER}`, borderRadius: 4, minWidth: 0 }}>
              <div style={{ color: AMBER, fontWeight: 600, fontSize: 11 }}>{t("polyforge.chat.cannotStart")}</div>
              <div data-pf-chat-cannot-start-reason="" style={{ color: "var(--text)", fontSize: 11, whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{cannotStart.text}</div>
              <CopyableCommands wi={wi} profile={profile} commands={cannotStart.commands} />
            </div>
          )}
        </>
      ) : (
        <div data-pf-chat-closed={state.reason} style={{ display: "flex", flexDirection: "column", gap: 6, minWidth: 0 }}>
          <div style={{ color: state.commands.length === 0 && state.reason !== "offline" && state.reason !== "unknown" ? "var(--text-dim)" : AMBER, fontSize: 11, overflowWrap: "anywhere" }}>
            {t(`polyforge.chat.closed.${state.reason}`)}
          </div>
          {wi && <CopyableCommands wi={wi} profile={profile} commands={state.commands} />}
        </div>
      )}
    </section>
  );
}
