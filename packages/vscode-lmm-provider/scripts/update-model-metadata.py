#!/usr/bin/env python3
"""Build an offline reference snapshot from a reviewed models.dev API response."""
import argparse
import hashlib
import json
from pathlib import Path

PROVIDERS = ("openai", "anthropic", "google", "deepseek", "mistral", "alibaba", "minimax", "zai", "xai")
# Older directory records can describe embedding vectors as text output.
NON_CHAT_IDS = (
    "dall-e-2", "dall-e-3", "whisper-1", "tts-1", "tts-1-hd",
    "gpt-4o-mini-tts", "gpt-4o-transcribe", "gpt-4o-mini-transcribe",
    "gpt-4o-transcribe-diarize", "gemini-embedding-001", "gemini-embedding-2",
    "rerank-v3.5", "rerank-english-v3.0", "rerank-multilingual-v3.0",
)
NON_CHAT_FAMILIES = {"text-embedding", "mistral-embed", "gpt-image"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("--retrieved", required=True)
    parser.add_argument("--revision", required=True)
    args = parser.parse_args()
    raw = args.source.read_bytes()
    data = json.loads(raw)
    models = {}
    for provider in PROVIDERS:
        for model_id, row in sorted(data[provider]["models"].items()):
            assert row["id"] == model_id
            limit, modalities = row.get("limit", {}), row.get("modalities", {})
            kind = row.get("type", "chat")
            if model_id in NON_CHAT_IDS or row.get("family") in NON_CHAT_FAMILIES:
                kind = "non-chat"
            model = {
                "source": f"{provider}/{model_id}", "type": kind,
                "family": row.get("family"), "context": limit.get("context"),
                "input": limit.get("input"), "output": limit.get("output"),
                "inputModalities": modalities.get("input"),
                "outputModalities": modalities.get("output"),
                "reasoning": row.get("reasoning"), "toolCalling": row.get("tool_call"),
            }
            models.setdefault(model_id, []).append(model)
    result = {
        "schemaVersion": 2, "source": "https://models.dev/api.json",
        "sourceSha256": hashlib.sha256(raw).hexdigest(),
        "revision": args.revision, "retrieved": args.retrieved,
        "providers": list(PROVIDERS), "nonChatIds": list(NON_CHAT_IDS), "models": models,
    }
    destination = Path(__file__).resolve().parents[1] / "src/model-limits.json"
    destination.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(f"Wrote {sum(map(len, models.values()))} exact provider/model references.")


if __name__ == "__main__":
    main()
