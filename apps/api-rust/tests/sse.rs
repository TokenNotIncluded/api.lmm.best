use lmm_api_rs::routes::sse::{SseFrameParser, SseParser};

#[test]
fn sse_parser_handles_incremental_frames_without_a_router() {
    let mut parser: SseParser = SseFrameParser::with_max_frame_bytes(1024);
    assert!(
        parser
            .push(b"event: message\ndata: {\"ok\":")
            .unwrap()
            .is_empty()
    );

    let frames = parser.push(b"true}\n\n").expect("complete SSE frame");
    assert_eq!(frames.len(), 1);
    assert_eq!(frames[0].event_name(), Some("message"));
    assert_eq!(frames[0].data(), "{\"ok\":true}");
    assert!(!parser.has_unfinished_frame());
}
