//! Incremental Server-Sent Events framing and loss-aware event policy.
//!
//! The parser deliberately stops at an empty line, rather than treating each
//! `data:` line as an event.  A frame is retained only until it is dispatched;
//! the configured byte limit therefore bounds the parser's active memory use.

use std::mem;

use serde_json::Value;

/// Conservative default for one SSE frame.
pub const DEFAULT_MAX_FRAME_BYTES: usize = 8 * 1024 * 1024;

/// Stable loss code used when a cross-protocol converter cannot preserve an
/// event it does not understand.
pub const LOSS_UNKNOWN_EVENT: &str = "LOSS_UNKNOWN_EVENT";

/// Errors produced while framing or adapting an SSE stream.
#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
pub enum SseError {
    /// The active frame exceeded its independently configured byte limit.
    #[error("SSE frame exceeded configured limit of {limit} bytes (observed {observed} bytes)")]
    FrameTooLarge {
        /// Maximum number of bytes allowed for one frame.
        limit: usize,
        /// Number of bytes observed when the limit was crossed.
        observed: usize,
    },
    /// A field that the SSE protocol requires to be UTF-8 was not valid UTF-8.
    #[error("SSE {field} field is not valid UTF-8")]
    InvalidUtf8 {
        /// Field whose value could not be decoded.
        field: &'static str,
    },
    /// An operation was attempted after a prior parser error.
    #[error("SSE parser is in a failed state")]
    ParserFailed,
    /// Feeding bytes after EOF is not permitted.
    #[error("SSE parser has already reached EOF")]
    AlreadyFinished,
    /// The existing relay event interface cannot carry a non-JSON data field.
    #[error("SSE frame {frame} has no JSON payload")]
    NonJsonPayload {
        /// Zero-based frame number in the parsed stream.
        frame: usize,
    },
    /// The existing relay event interface cannot carry this frame metadata.
    #[error("SSE frame {frame} contains unsupported {field} metadata")]
    UnsupportedMetadata {
        /// Zero-based frame number in the parsed stream.
        frame: usize,
        /// Metadata kind which would otherwise be lost.
        field: &'static str,
    },
    /// A JSON-looking data field could not be decoded as JSON.
    #[error("SSE frame {frame} data is not valid JSON")]
    InvalidJson {
        /// Zero-based frame number in the parsed stream.
        frame: usize,
    },
    /// A relay adapter chose to reject an unterminated frame rather than
    /// silently discard its payload at EOF.
    #[error("SSE stream ended before the current frame was terminated")]
    UnterminatedFrame,
}

/// EOF behavior for a frame whose terminating empty line has not arrived.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub enum SseEofMode {
    /// Follow the WHATWG event-stream algorithm: only an empty line dispatches
    /// an event, so an unterminated final frame is discarded at EOF.
    #[default]
    Strict,
    /// Flush an unterminated final frame for explicitly selected legacy
    /// upstream compatibility.
    FlushUnterminated,
}

/// One complete SSE event frame.
///
/// `raw` contains the original bytes, including line endings and the empty
/// line which dispatched the frame when one was present.  Keeping raw bytes
/// here allows same-protocol callers to retain unknown event data without
/// reparsing or logging the body.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SseFrame {
    /// Optional event name; absent means the SSE default event type.
    pub event: Option<String>,
    /// Last `id` field in the frame, including an explicitly empty id.
    pub id: Option<String>,
    /// Last `retry` field in milliseconds.
    pub retry: Option<u64>,
    /// All `data` field values joined with a single newline.
    pub data: String,
    /// Comment values, in source order.
    pub comments: Vec<String>,
    /// Names of fields not interpreted by this parser.
    pub unknown_fields: Vec<String>,
    /// Whether at least one `data` field occurred, including `data:` with an
    /// empty value.
    pub has_data: bool,
    /// Original frame bytes, bounded by the parser's frame limit.
    pub raw: Vec<u8>,
}

impl SseFrame {
    /// Returns true for the conventional OpenAI/Gemini terminal marker.
    #[must_use]
    pub fn is_done(&self) -> bool {
        self.has_data && self.data == "[DONE]"
    }

    /// Returns the event name as a borrowed string, if one was supplied.
    #[must_use]
    pub fn event_name(&self) -> Option<&str> {
        self.event.as_deref()
    }

    /// Returns the frame data.
    #[must_use]
    pub fn data(&self) -> &str {
        &self.data
    }

    /// Returns the retry delay in milliseconds.
    #[must_use]
    pub fn retry_ms(&self) -> Option<u64> {
        self.retry
    }

    /// Returns whether this frame contains metadata which the legacy relay
    /// event DTO cannot represent.
    #[must_use]
    pub fn has_unrepresentable_metadata(&self) -> bool {
        self.id.is_some()
            || self.retry.is_some()
            || !self.comments.is_empty()
            || !self.unknown_fields.is_empty()
    }
}

/// Incremental SSE parser.  Feed arbitrary byte slices; frames are returned
/// only after an empty line or an explicit EOF flush.
#[derive(Debug)]
pub struct SseFrameParser {
    max_frame_bytes: usize,
    eof_mode: SseEofMode,
    line: Vec<u8>,
    raw: Vec<u8>,
    event: Option<String>,
    id: Option<String>,
    retry: Option<u64>,
    data: String,
    comments: Vec<String>,
    unknown_fields: Vec<String>,
    has_data: bool,
    has_content: bool,
    pending_cr: bool,
    bom_prefix: Vec<u8>,
    bom_checked: bool,
    failed: bool,
    finished: bool,
}

/// Short alias for callers that refer to the component as an SSE parser.
pub type SseParser = SseFrameParser;

/// Short alias for callers that refer to a complete frame as an SSE event.
pub type SseEvent = SseFrame;

impl Default for SseFrameParser {
    fn default() -> Self {
        Self::new(DEFAULT_MAX_FRAME_BYTES)
    }
}

impl SseFrameParser {
    /// Creates a parser with an explicit maximum frame size in bytes.
    ///
    /// A zero limit is valid and permits only an empty stream; any non-empty
    /// frame byte then produces [`SseError::FrameTooLarge`].
    #[must_use]
    pub fn new(max_frame_bytes: usize) -> Self {
        Self {
            max_frame_bytes,
            eof_mode: SseEofMode::Strict,
            line: Vec::new(),
            raw: Vec::new(),
            event: None,
            id: None,
            retry: None,
            data: String::new(),
            comments: Vec::new(),
            unknown_fields: Vec::new(),
            has_data: false,
            has_content: false,
            pending_cr: false,
            bom_prefix: Vec::new(),
            bom_checked: false,
            failed: false,
            finished: false,
        }
    }

    /// Alias for [`Self::new`] useful at configuration call sites.
    #[must_use]
    pub fn with_max_frame_bytes(max_frame_bytes: usize) -> Self {
        Self::new(max_frame_bytes)
    }

    /// Selects how an unterminated final frame is handled at EOF.
    #[must_use]
    pub fn with_eof_mode(mut self, eof_mode: SseEofMode) -> Self {
        self.eof_mode = eof_mode;
        self
    }

    /// Returns the configured EOF behavior.
    #[must_use]
    pub const fn eof_mode(&self) -> SseEofMode {
        self.eof_mode
    }

    /// Returns the configured maximum frame size.
    #[must_use]
    pub const fn max_frame_bytes(&self) -> usize {
        self.max_frame_bytes
    }

    /// Returns the number of wire bytes retained for the active frame.
    ///
    /// This is bounded by [`Self::max_frame_bytes`] and is intended for
    /// diagnostics and invariant tests; it does not include the small
    /// decoded-field allocations which are also derived from that frame.
    #[must_use]
    pub fn buffered_frame_bytes(&self) -> usize {
        self.raw.len()
    }

    /// Returns whether bytes belonging to a frame are waiting for a dispatch
    /// delimiter.  This is useful to adapters that need an explicit error
    /// instead of the strict parser's standards-compliant EOF discard.
    #[must_use]
    pub fn has_unfinished_frame(&self) -> bool {
        if !self.bom_prefix.is_empty() {
            // Before three bytes arrive, the parser cannot yet decide
            // whether the prefix is a UTF-8 BOM.  A prefix made solely of
            // line-ending bytes is nevertheless already a complete empty
            // line (or several), not an unterminated event.
            return !self
                .bom_prefix
                .iter()
                .all(|byte| *byte == b'\r' || *byte == b'\n');
        }
        if !self.line.is_empty() {
            return true;
        }
        if !self.has_content {
            return false;
        }
        if !self.pending_cr {
            return true;
        }
        // A CR at EOF terminates a line.  It is a dispatch delimiter only
        // when that line is empty; the preceding byte distinguishes
        // `data: value\r` from `data: value\r\r` (and `...\n\r`).
        !self
            .raw
            .len()
            .checked_sub(2)
            .and_then(|index| self.raw.get(index))
            .is_some_and(|byte| *byte == b'\r' || *byte == b'\n')
    }

    /// Feeds an arbitrary byte chunk and returns all frames completed by it.
    ///
    /// No assumption is made about chunk boundaries: a UTF-8 code point,
    /// field line, CRLF pair, or complete frame may all be split across calls.
    pub fn feed(&mut self, chunk: &[u8]) -> Result<Vec<SseFrame>, SseError> {
        self.ensure_active()?;
        let result = self.feed_inner(chunk);
        if result.is_err() {
            self.failed = true;
        }
        result
    }

    /// Alias for [`Self::feed`].
    pub fn push(&mut self, chunk: &[u8]) -> Result<Vec<SseFrame>, SseError> {
        self.feed(chunk)
    }

    /// Flushes the final frame according to the SSE EOF rule.
    pub fn finish(&mut self) -> Result<Vec<SseFrame>, SseError> {
        self.ensure_active()?;
        let result = self.finish_inner();
        if result.is_err() {
            self.failed = true;
        } else {
            self.finished = true;
        }
        result
    }

    /// Alias for [`Self::finish`].
    pub fn eof(&mut self) -> Result<Vec<SseFrame>, SseError> {
        self.finish()
    }

    fn ensure_active(&self) -> Result<(), SseError> {
        if self.failed {
            Err(SseError::ParserFailed)
        } else if self.finished {
            Err(SseError::AlreadyFinished)
        } else {
            Ok(())
        }
    }

    fn feed_inner(&mut self, chunk: &[u8]) -> Result<Vec<SseFrame>, SseError> {
        let mut frames = Vec::new();
        for &byte in chunk {
            if !self.bom_checked {
                self.bom_prefix.push(byte);
                if self.bom_prefix.len() < 3 {
                    continue;
                }
                if self.bom_prefix == b"\xef\xbb\xbf" {
                    let bom = mem::take(&mut self.bom_prefix);
                    self.bom_checked = true;
                    // The BOM is ignored by the SSE decoder, but it remains
                    // part of the first raw frame so same-protocol
                    // passthrough can forward the complete wire bytes.
                    for byte in bom {
                        self.push_raw(byte)?;
                    }
                    continue;
                }
                self.bom_checked = true;
                let prefix = mem::take(&mut self.bom_prefix);
                for prefix_byte in prefix {
                    self.process_byte(prefix_byte, &mut frames)?;
                }
                continue;
            }
            self.process_byte(byte, &mut frames)?;
        }
        Ok(frames)
    }

    fn process_byte(&mut self, byte: u8, frames: &mut Vec<SseFrame>) -> Result<(), SseError> {
        if self.pending_cr {
            self.pending_cr = false;
            if byte == b'\n' {
                self.push_raw(byte)?;
                if let Some(frame) = self.complete_line()? {
                    frames.push(frame);
                }
                return Ok(());
            }
            if let Some(frame) = self.complete_line()? {
                frames.push(frame);
            }
        }

        if byte == b'\r' {
            self.push_raw(byte)?;
            self.pending_cr = true;
        } else if byte == b'\n' {
            self.push_raw(byte)?;
            if let Some(frame) = self.complete_line()? {
                frames.push(frame);
            }
        } else {
            self.push_raw(byte)?;
            self.line.push(byte);
        }
        Ok(())
    }

    fn finish_inner(&mut self) -> Result<Vec<SseFrame>, SseError> {
        let mut frames = Vec::new();
        if !self.bom_checked {
            self.bom_checked = true;
            let prefix = mem::take(&mut self.bom_prefix);
            for byte in prefix {
                self.process_byte(byte, &mut frames)?;
            }
        }
        if self.pending_cr {
            self.pending_cr = false;
            if let Some(frame) = self.complete_line()? {
                frames.push(frame);
            }
        }
        if !self.line.is_empty()
            && let Some(frame) = self.complete_line()?
        {
            frames.push(frame);
        }
        if self.eof_mode == SseEofMode::FlushUnterminated && self.has_content {
            if let Some(frame) = self.dispatch_frame() {
                frames.push(frame);
            }
        } else {
            self.reset_frame();
        }
        Ok(frames)
    }

    fn push_raw(&mut self, byte: u8) -> Result<(), SseError> {
        let observed = self.raw.len().saturating_add(1);
        if observed > self.max_frame_bytes {
            return Err(SseError::FrameTooLarge {
                limit: self.max_frame_bytes,
                observed,
            });
        }
        self.raw.push(byte);
        Ok(())
    }

    fn complete_line(&mut self) -> Result<Option<SseFrame>, SseError> {
        if self.line.is_empty() {
            return Ok(if self.has_content {
                self.dispatch_frame()
            } else {
                self.reset_frame();
                None
            });
        }
        let line = mem::take(&mut self.line);
        self.has_content = true;
        self.parse_line(&line)?;
        Ok(None)
    }

    fn parse_line(&mut self, line: &[u8]) -> Result<(), SseError> {
        if line[0] == b':' {
            let value = value_without_optional_space(&line[1..]);
            self.comments.push(decode_utf8(value, "comment")?);
            return Ok(());
        }

        let (name, value) = line
            .iter()
            .position(|byte| *byte == b':')
            .map_or((line, &[][..]), |separator| {
                (&line[..separator], &line[separator + 1..])
            });
        let value = value_without_optional_space(value);
        match name {
            b"data" => {
                let value = decode_utf8(value, "data")?;
                if self.has_data {
                    self.data.push('\n');
                }
                self.data.push_str(&value);
                self.has_data = true;
            }
            b"event" => {
                self.event = Some(decode_utf8(value, "event")?);
            }
            b"id" => {
                let value = decode_utf8(value, "id")?;
                // The SSE specification ignores an id containing U+0000.
                if !value.contains('\0') {
                    self.id = Some(value);
                }
            }
            b"retry" => {
                let value = decode_utf8(value, "retry")?;
                // WHATWG says malformed retry values are ignored, while a
                // valid value consists only of ASCII decimal digits.
                if let Some(retry) = parse_retry_millis(&value) {
                    self.retry = Some(retry);
                }
            }
            _ => {
                self.unknown_fields.push(decode_utf8(name, "field name")?);
            }
        }
        Ok(())
    }

    fn dispatch_frame(&mut self) -> Option<SseFrame> {
        if !self.has_content {
            self.reset_frame();
            return None;
        }
        let frame = SseFrame {
            event: self.event.take(),
            id: self.id.take(),
            retry: self.retry.take(),
            data: mem::take(&mut self.data),
            comments: mem::take(&mut self.comments),
            unknown_fields: mem::take(&mut self.unknown_fields),
            has_data: self.has_data,
            raw: mem::take(&mut self.raw),
        };
        self.reset_frame();
        Some(frame)
    }

    fn reset_frame(&mut self) {
        self.line.clear();
        self.raw.clear();
        self.event = None;
        self.id = None;
        self.retry = None;
        self.data.clear();
        self.comments.clear();
        self.unknown_fields.clear();
        self.has_data = false;
        self.has_content = false;
    }
}

fn value_without_optional_space(value: &[u8]) -> &[u8] {
    if value.first() == Some(&b' ') {
        &value[1..]
    } else {
        value
    }
}

fn decode_utf8(value: &[u8], field: &'static str) -> Result<String, SseError> {
    String::from_utf8(value.to_vec()).map_err(|_| SseError::InvalidUtf8 { field })
}

fn parse_retry_millis(value: &str) -> Option<u64> {
    if value.is_empty() || !value.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    value.parse().ok()
}

/// Parses a complete byte buffer using strict WHATWG EOF handling.
///
/// A final frame without an empty-line delimiter is discarded.  Use
/// [`parse_sse_frames_lenient`] only when an upstream compatibility decision
/// explicitly permits flushing that frame.
pub fn parse_sse_frames(input: &[u8], max_frame_bytes: usize) -> Result<Vec<SseFrame>, SseError> {
    let mut parser = SseFrameParser::new(max_frame_bytes);
    let mut frames = parser.feed(input)?;
    frames.extend(parser.finish()?);
    Ok(frames)
}

/// Parses strict SSE and turns a discarded unterminated final frame into an
/// explicit error for adapters where lossless relay behavior is required.
pub fn parse_sse_frames_rejecting_unterminated(
    input: &[u8],
    max_frame_bytes: usize,
) -> Result<Vec<SseFrame>, SseError> {
    let mut parser = SseFrameParser::new(max_frame_bytes);
    let mut frames = parser.feed(input)?;
    let unfinished = parser.has_unfinished_frame();
    frames.extend(parser.finish()?);
    if unfinished {
        return Err(SseError::UnterminatedFrame);
    }
    Ok(frames)
}

/// Parses a complete byte buffer using explicit legacy EOF flushing.
pub fn parse_sse_frames_lenient(
    input: &[u8],
    max_frame_bytes: usize,
) -> Result<Vec<SseFrame>, SseError> {
    let mut parser =
        SseFrameParser::new(max_frame_bytes).with_eof_mode(SseEofMode::FlushUnterminated);
    let mut frames = parser.feed(input)?;
    frames.extend(parser.finish()?);
    Ok(frames)
}

/// Classification of an event unknown to a cross-protocol converter.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum UnknownEventClass {
    /// Event is expected not to change generated content or stream state.
    Metadata,
    /// Event may carry content or content-block state.
    Content,
    /// Event may change termination or error state.
    Termination,
}

/// Action a converter must take for an unknown event.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum UnknownEventAction {
    /// Same-protocol forwarding keeps the original frame and event name.
    Preserve,
    /// Record [`LOSS_UNKNOWN_EVENT`] and continue the conversion.
    RecordLossAndContinue,
    /// Enter a degraded state or return a conversion error.
    DegradedOrError,
}

/// Explicit decision returned by [`unknown_event_decision`].
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct UnknownEventDecision {
    /// Heuristic class used by the cross-protocol policy.
    pub class: UnknownEventClass,
    /// Required action for the caller.
    pub action: UnknownEventAction,
    /// Stable loss code, present for cross-protocol handling.
    pub loss_code: Option<&'static str>,
}

/// Chooses an explicit policy for an event not known by the target protocol.
///
/// Same-protocol callers preserve every frame.  Cross-protocol callers may
/// continue only for conservative metadata names; content and termination
/// names require degraded/error handling.
#[must_use]
pub fn unknown_event_decision(
    same_protocol: bool,
    event_name: Option<&str>,
) -> UnknownEventDecision {
    let class = classify_unknown_event(event_name);
    if same_protocol {
        return UnknownEventDecision {
            class,
            action: UnknownEventAction::Preserve,
            loss_code: None,
        };
    }
    let action = match class {
        UnknownEventClass::Metadata => UnknownEventAction::RecordLossAndContinue,
        UnknownEventClass::Content | UnknownEventClass::Termination => {
            UnknownEventAction::DegradedOrError
        }
    };
    UnknownEventDecision {
        class,
        action,
        loss_code: Some(LOSS_UNKNOWN_EVENT),
    }
}

fn classify_unknown_event(event_name: Option<&str>) -> UnknownEventClass {
    let Some(event_name) = event_name else {
        return UnknownEventClass::Content;
    };
    let name = event_name.to_ascii_lowercase();
    // A termination signal must win over a metadata-looking prefix or
    // suffix.  Continuing after an event such as `metadata.complete` can
    // silently lose the stream's final state, which is more dangerous than
    // conservatively entering degraded handling.
    let mut parts = name.split(|character: char| !character.is_ascii_alphanumeric());
    if parts.clone().any(|part| {
        matches!(
            part,
            "error"
                | "errors"
                | "done"
                | "finish"
                | "finished"
                | "complete"
                | "completed"
                | "completion"
                | "terminate"
                | "terminated"
                | "close"
                | "closed"
                | "stop"
                | "stopped"
                | "halt"
                | "halted"
                | "end"
                | "ended"
        )
    }) {
        return UnknownEventClass::Termination;
    }
    if matches!(
        name.as_str(),
        "ping"
            | "keep_alive"
            | "keepalive"
            | "heartbeat"
            | "metadata"
            | "message_metadata"
            | "usage"
            | "trace"
            | "debug"
    ) || parts.any(|part| {
        matches!(
            part,
            "ping" | "keepalive" | "heartbeat" | "metadata" | "usage" | "trace" | "debug"
        )
    }) {
        return UnknownEventClass::Metadata;
    }
    UnknownEventClass::Content
}

/// JSON event representation used by adapters that still expose the legacy
/// `RelaySseEvent { kind, payload }` interface.
#[derive(Clone, Debug, PartialEq)]
pub struct JsonSseEvent {
    /// Original event name.
    pub event: Option<String>,
    /// Decoded JSON data.
    pub payload: Value,
}

/// Converts parsed frames to the legacy JSON event subset without dropping
/// unsupported metadata or malformed data.
pub fn json_events_from_frames(frames: &[SseFrame]) -> Result<Vec<JsonSseEvent>, SseError> {
    let mut events = Vec::new();
    for (index, frame) in frames.iter().enumerate() {
        if frame.is_done() {
            if frame.event.is_some() {
                return Err(SseError::UnsupportedMetadata {
                    frame: index,
                    field: "event",
                });
            }
            if frame.has_unrepresentable_metadata() {
                return Err(unsupported_frame_metadata(frame, index));
            }
            continue;
        }
        if frame.id.is_some() {
            return Err(SseError::UnsupportedMetadata {
                frame: index,
                field: "id",
            });
        }
        if frame.retry.is_some() {
            return Err(SseError::UnsupportedMetadata {
                frame: index,
                field: "retry",
            });
        }
        if !frame.comments.is_empty() {
            return Err(SseError::UnsupportedMetadata {
                frame: index,
                field: "comment",
            });
        }
        if !frame.unknown_fields.is_empty() {
            return Err(SseError::UnsupportedMetadata {
                frame: index,
                field: "unknown field",
            });
        }
        if !frame.has_data || frame.data.is_empty() {
            return Err(SseError::NonJsonPayload { frame: index });
        }
        let Ok(payload) = serde_json::from_str::<Value>(&frame.data) else {
            return Err(SseError::InvalidJson { frame: index });
        };
        events.push(JsonSseEvent {
            event: frame.event.clone(),
            payload,
        });
    }
    Ok(events)
}

fn unsupported_frame_metadata(frame: &SseFrame, index: usize) -> SseError {
    if frame.id.is_some() {
        SseError::UnsupportedMetadata {
            frame: index,
            field: "id",
        }
    } else if frame.retry.is_some() {
        SseError::UnsupportedMetadata {
            frame: index,
            field: "retry",
        }
    } else if !frame.comments.is_empty() {
        SseError::UnsupportedMetadata {
            frame: index,
            field: "comment",
        }
    } else {
        SseError::UnsupportedMetadata {
            frame: index,
            field: "unknown field",
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{
        DEFAULT_MAX_FRAME_BYTES, SseEofMode, SseError, SseFrameParser, UnknownEventAction,
        UnknownEventClass, json_events_from_frames, parse_sse_frames,
        parse_sse_frames_rejecting_unterminated, unknown_event_decision,
    };

    #[test]
    fn parser_joins_multiple_data_lines_and_preserves_event_metadata() {
        let input = b"event: update\nid: abc\nretry: 1500\n: note\ndata: first\ndata: second\n\n";
        let frames = parse_sse_frames(input, DEFAULT_MAX_FRAME_BYTES).expect("valid SSE");
        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].event.as_deref(), Some("update"));
        assert_eq!(frames[0].id.as_deref(), Some("abc"));
        assert_eq!(frames[0].retry, Some(1500));
        assert_eq!(frames[0].comments, vec!["note"]);
        assert_eq!(frames[0].data, "first\nsecond");
        assert_eq!(frames[0].raw, input);
    }

    #[test]
    fn parser_accepts_crlf_and_field_values_without_a_space() {
        let frames = parse_sse_frames(
            b"event:update\r\ndata:[DONE]\r\n\r\n",
            DEFAULT_MAX_FRAME_BYTES,
        )
        .expect("valid CRLF SSE");
        assert_eq!(frames[0].event.as_deref(), Some("update"));
        assert!(frames[0].is_done());
    }

    #[test]
    fn parser_accepts_carriage_return_line_endings() {
        let frames = parse_sse_frames(b"data: {\"ok\":true}\r\r", DEFAULT_MAX_FRAME_BYTES)
            .expect("valid CR SSE");
        assert_eq!(frames[0].data, "{\"ok\":true}");
    }

    #[test]
    fn parser_emits_empty_data_frame() {
        let frames = parse_sse_frames(b"data:\n\n", DEFAULT_MAX_FRAME_BYTES).expect("valid SSE");
        assert_eq!(frames[0].data, "");
        assert!(frames[0].has_data);
        assert!(!frames[0].is_done());
    }

    #[test]
    fn parser_dispatches_comment_only_frames_without_losing_comment_data() {
        let frames = parse_sse_frames(b": keep-alive\n\n", DEFAULT_MAX_FRAME_BYTES)
            .expect("valid comment frame");
        assert_eq!(frames[0].comments, vec!["keep-alive"]);
        assert!(!frames[0].has_data);
    }

    #[test]
    fn parser_strict_eof_does_not_dispatch_an_unterminated_frame() {
        let frames = parse_sse_frames(b"data: [DONE]", DEFAULT_MAX_FRAME_BYTES).expect("EOF");
        assert!(frames.is_empty());
    }

    #[test]
    fn parser_can_explicitly_flush_an_unterminated_legacy_frame() {
        let mut parser = SseFrameParser::new(DEFAULT_MAX_FRAME_BYTES)
            .with_eof_mode(SseEofMode::FlushUnterminated);
        let frames = parser.feed(b"data: [DONE]").expect("feed");
        assert!(frames.is_empty());
        let frames = parser.finish().expect("EOF flush");
        assert_eq!(frames.len(), 1);
        assert!(frames[0].is_done());
    }

    #[test]
    fn parser_handles_every_single_byte_chunk_boundary() {
        let input = b"event: update\r\ndata: {\"x\":\"y\"}\r\n\r\ndata: [DONE]\n\n";
        let mut parser = SseFrameParser::new(DEFAULT_MAX_FRAME_BYTES);
        let mut frames = Vec::new();
        for byte in input {
            frames.extend(parser.feed(std::slice::from_ref(byte)).expect("byte feed"));
        }
        frames.extend(parser.finish().expect("EOF flush"));
        assert_eq!(frames.len(), 2);
        assert_eq!(frames[0].data, "{\"x\":\"y\"}");
        assert!(frames[1].is_done());
    }

    #[test]
    fn parser_ignores_invalid_retry_without_discarding_the_frame() {
        let frames = parse_sse_frames(
            b"retry: 100\nretry: +1\nretry: \xEF\xBC\x91\ndata: {}\n\n",
            DEFAULT_MAX_FRAME_BYTES,
        )
        .expect("invalid retry is ignored");
        assert_eq!(frames[0].retry, Some(100));
    }

    #[test]
    fn parser_ignores_an_id_containing_nul() {
        let frames = parse_sse_frames(
            b"id: retained\nid: bad\x00id\ndata: {}\n\n",
            DEFAULT_MAX_FRAME_BYTES,
        )
        .expect("NUL id is ignored");
        assert_eq!(frames[0].id.as_deref(), Some("retained"));
    }

    #[test]
    fn parser_rejects_frames_over_the_configured_limit() {
        let error = parse_sse_frames(b"data: 123\n\n", 5).unwrap_err();
        assert_eq!(
            error,
            SseError::FrameTooLarge {
                limit: 5,
                observed: 6
            }
        );
    }

    #[test]
    fn parser_strict_eof_discards_a_frame_after_a_complete_data_line() {
        let mut parser = SseFrameParser::new(DEFAULT_MAX_FRAME_BYTES);
        assert!(parser.feed(b"data: value\n").expect("feed").is_empty());
        let frames = parser.finish().expect("EOF flush");
        assert!(frames.is_empty());
    }

    #[test]
    fn lossless_adapter_mode_rejects_an_unterminated_frame() {
        assert_eq!(
            parse_sse_frames_rejecting_unterminated(b"data: value\n", DEFAULT_MAX_FRAME_BYTES)
                .unwrap_err(),
            SseError::UnterminatedFrame
        );
        assert_eq!(
            parse_sse_frames_rejecting_unterminated(b"data: value\r", DEFAULT_MAX_FRAME_BYTES)
                .unwrap_err(),
            SseError::UnterminatedFrame
        );
        assert_eq!(
            parse_sse_frames_rejecting_unterminated(b"data: value\r\r", DEFAULT_MAX_FRAME_BYTES)
                .expect("CR empty-line delimiter")
                .len(),
            1
        );
    }

    #[test]
    fn lossless_adapter_counts_partial_bom_and_plain_prefix_as_unterminated() {
        for prefix in [b"\xef".as_slice(), b"\xef\xbb".as_slice()] {
            let mut parser = SseFrameParser::new(DEFAULT_MAX_FRAME_BYTES);
            parser.feed(prefix).expect("partial BOM");
            assert!(parser.has_unfinished_frame());
        }
        for prefix in [b"d".as_slice(), b"da".as_slice()] {
            assert_eq!(
                parse_sse_frames_rejecting_unterminated(prefix, DEFAULT_MAX_FRAME_BYTES)
                    .unwrap_err(),
                SseError::UnterminatedFrame
            );
        }
    }

    #[test]
    fn lossless_adapter_does_not_mistake_short_empty_lines_for_partial_bom() {
        for input in [b"\n".as_slice(), b"\r".as_slice(), b"\r\n".as_slice()] {
            assert!(
                parse_sse_frames_rejecting_unterminated(input, DEFAULT_MAX_FRAME_BYTES)
                    .expect("short empty line")
                    .is_empty()
            );
        }
    }

    #[test]
    fn parser_ignores_a_utf8_bom_once_even_when_split_across_chunks() {
        let mut parser = SseFrameParser::new(DEFAULT_MAX_FRAME_BYTES);
        assert!(parser.feed(b"\xef").expect("BOM prefix").is_empty());
        assert!(parser.feed(b"\xbb").expect("BOM prefix").is_empty());
        let frames = parser.feed(b"\xbfdata: {}\n\n").expect("BOM and frame");
        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].data, "{}");
        assert_eq!(frames[0].raw, b"\xef\xbb\xbfdata: {}\n\n");
    }

    #[test]
    fn parser_active_storage_stays_within_the_frame_limit_during_slow_feed() {
        let limit = 64;
        let input = b"data: bounded\n\n";
        let mut parser = SseFrameParser::new(limit);
        for byte in input {
            let frames = parser.feed(std::slice::from_ref(byte)).expect("byte feed");
            assert!(parser.buffered_frame_bytes() <= limit);
            assert!(parser.line.len() <= limit);
            assert!(frames.len() <= 1);
        }
    }

    #[test]
    fn same_protocol_unknown_events_are_preserved() {
        let decision = unknown_event_decision(true, Some("future_event"));
        assert_eq!(decision.class, UnknownEventClass::Content);
        assert_eq!(decision.action, UnknownEventAction::Preserve);
        assert_eq!(decision.loss_code, None);
    }

    #[test]
    fn cross_protocol_metadata_unknown_events_record_loss_and_continue() {
        let decision = unknown_event_decision(false, Some("message_metadata"));
        assert_eq!(decision.class, UnknownEventClass::Metadata);
        assert_eq!(decision.action, UnknownEventAction::RecordLossAndContinue);
        assert_eq!(decision.loss_code, Some("LOSS_UNKNOWN_EVENT"));
    }

    #[test]
    fn cross_protocol_content_unknown_events_require_degraded_handling() {
        let decision = unknown_event_decision(false, Some("future_content"));
        assert_eq!(decision.class, UnknownEventClass::Content);
        assert_eq!(decision.action, UnknownEventAction::DegradedOrError);
    }

    #[test]
    fn cross_protocol_punctuation_delimited_metadata_events_record_loss() {
        for event_name in ["response.metadata", "response-usage", "metadata.update"] {
            let decision = unknown_event_decision(false, Some(event_name));
            assert_eq!(decision.class, UnknownEventClass::Metadata, "{event_name}");
            assert_eq!(
                decision.action,
                UnknownEventAction::RecordLossAndContinue,
                "{event_name}"
            );
        }
    }

    #[test]
    fn cross_protocol_termination_takes_precedence_over_metadata_name() {
        let decision = unknown_event_decision(false, Some("metadata.complete"));
        assert_eq!(decision.class, UnknownEventClass::Termination);
        assert_eq!(decision.action, UnknownEventAction::DegradedOrError);
    }

    #[test]
    fn unknown_event_words_are_matched_as_tokens_not_substrings() {
        for event_name in ["abandoned_content", "keepsake_content", "keep_content"] {
            let decision = unknown_event_decision(false, Some(event_name));
            assert_eq!(decision.class, UnknownEventClass::Content, "{event_name}");
            assert_eq!(
                decision.action,
                UnknownEventAction::DegradedOrError,
                "{event_name}"
            );
        }
    }

    #[test]
    fn legacy_json_adapter_preserves_unknown_event_name() {
        let frames = parse_sse_frames(
            b"event: future_event\ndata: {\"value\":1}\n\ndata: [DONE]\n\n",
            DEFAULT_MAX_FRAME_BYTES,
        )
        .expect("valid SSE");
        let events = json_events_from_frames(&frames).expect("JSON-compatible frames");
        assert_eq!(events[0].event.as_deref(), Some("future_event"));
        assert_eq!(events[0].payload["value"], 1);
    }

    #[test]
    fn legacy_json_adapter_rejects_metadata_instead_of_dropping_it() {
        let frames = parse_sse_frames(b"id: abc\ndata: {\"value\":1}\n\n", DEFAULT_MAX_FRAME_BYTES)
            .expect("valid SSE");
        assert_eq!(
            json_events_from_frames(&frames).unwrap_err(),
            SseError::UnsupportedMetadata {
                frame: 0,
                field: "id"
            }
        );
    }

    #[test]
    fn legacy_json_adapter_rejects_an_event_name_on_done() {
        let frames = parse_sse_frames(
            b"event: future_terminal\ndata: [DONE]\n\n",
            DEFAULT_MAX_FRAME_BYTES,
        )
        .expect("valid SSE");
        assert_eq!(
            json_events_from_frames(&frames).unwrap_err(),
            SseError::UnsupportedMetadata {
                frame: 0,
                field: "event"
            }
        );
    }
}
