# API Reference Documents

Fresh official API documentation organized by official path structure.

Downloaded on 2026-05-23.

## Categories

- `openai-chat-completions/`: OpenAI Chat Completions API
- `openai-responses/`: OpenAI Responses API
- `gemini-api/`: Gemini API
- `anthropic-messages/`: Anthropic Messages API

## Structure

- `official/`: original official indexes, full exports, or specifications.
- `docs/`: documentation pages organized by upstream URL path.
- `endpoints/`: OpenAPI path extracts for OpenAI API categories.
- `raw/`: exact Gemini URL-derived raw file set.
- `html-fallback/`: official Anthropic rendered pages for indexed URLs absent from `llms-full.txt`.

## Counts

- OpenAI official docs per category: 143 pages
- OpenAI Chat Completions endpoint files: 3
- OpenAI Responses endpoint files: 6
- Gemini API docs/raw files: 168/168
- Anthropic indexed URLs represented: 1541/1541 (`1406` Markdown, `135` HTML fallback, `0` unavailable)

Run `./verify.py` from this directory to validate.
