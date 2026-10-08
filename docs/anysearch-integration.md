# AnySearch integration

AnySearch is an opt-in web search provider for RAGFlow Chat, harness web search,
and Deep Research. Select `anysearch` and configure a non-blank
`anysearch_api_key`. RAGFlow never performs anonymous AnySearch searches and does
not borrow another provider's key. Existing provider defaults stay unchanged.

## Configuration

In assistant settings, select **AnySearch** and enter the key in the password
field. Optional settings include a capability tag such as `code.doc` and an
**Extract page content** switch, which is off by default. API clients can also
set vertical parameters in `prompt_config`:

```json
{
  "web_search_provider": "anysearch",
  "anysearch_api_key": "<configure securely; do not commit>",
  "anysearch_tag": "code.doc",
  "anysearch_params": {"library": "golang"},
  "anysearch_extract": false
}
```

Omit the tag and parameters for automatic routing. The tag must identify one
capability in `domain.sub_domain` form. Parameters must be an object; consult
the selected capability's current definitions before using them. JSON parameters
are available through the HTTP API and are preserved by the settings schema;
the form exposes the tag and extraction switch.

Keep keys in the application's existing protected configuration, or in a
process environment for manual tests. Do not put keys in URLs, shell arguments,
screenshots, source files, or diagnostic output.

## Architecture and API contract

- `resolveWebSearchProvider` reads and trims only `anysearch_api_key`. Invalid
  advanced option types disable the configuration rather than sending them.
- The shared `retrieveWebSearchWithTavily` dispatcher serves Chat and Deep
  Research. AnySearch-specific code lives in `web_search_anysearch.go`, keeping
  most changes separate from other providers' adapters.
- Search sends `POST https://api.anysearch.com/v1/search`, a Bearer key, and JSON
  `query` plus `max_results: 6`. Optional `tag` and `params` pass through.
- Responses must be JSON objects with an integer `code` explicitly equal to
  zero and an object `data`. Search requires `data.results` to be an array.
  Results use `title`, `url`, and non-blank `content`, with `snippet` fallback.
  Invalid HTTP(S) URLs, embedded URL credentials, and empty text are discarded.
  The six-result limit applies after filtering.
- `webSearchPayload` supplies RAGFlow's usual `chunks` / `doc_aggs` citation
  shape, with IDs prefixed `anysearch-` and the source URL preserved.
- When extraction is explicitly enabled, `POST /v1/extract` sends one `url`
  per usable search hit, at most six calls. Successful extracted text replaces
  search text; failures retain search text. Citation URLs remain the search
  URLs. Empty search hits are filtered before extraction.
- Discovery uses `GET /v1/sub-domains?domain=code&domain=finance` with repeated
  `domain` parameters. The Go discovery helper and opt-in live script support
  this contract. Discovery is not automatically fetched by chat or the form;
  there is no new public RAGFlow discovery endpoint.
- The shared HTTP transport caps responses at 4 MiB and uses a 30-second client
  timeout. AnySearch's client rejects redirects before authentication can be
  removed. Context cancellation propagates. AnySearch request/API errors are
  fixed safe messages: raw bodies, provider messages, transport exception text,
  and credentials are never included. No retries or anonymous fallback occur.

The request and response implementation was checked against the existing
[GPT Researcher AnySearch retriever](https://github.com/Harrison0223-svg/gpt-researcher/blob/af3befafc3fb2a2af51dffb6374a0b205dda8f5d/gpt_researcher/retrievers/anysearch/anysearch.py)
and the official [search](https://anysearch.com/docs/api-endpoints/v1-search),
[discovery](https://anysearch.com/docs/api-endpoints/v1-sub-domains), and
[extract](https://anysearch.com/docs/api-endpoints/v1-extract) documentation.
RAGFlow deliberately requires a key even though that retriever supports
anonymous access.

## Correspondence with merged You.com PR #18478

Baseline inspected: upstream main
`87a4eab513ce93738efd39dd0b97cc2a2ce5016c` (2026-10-08).
The [merged PR](https://github.com/infiniflow/ragflow/pull/18478) changed these
files. Current main has since removed the Python web-search implementation.
Root `AGENTS.md` directs new server functionality to Go and prohibits adding
Python code during this migration; the frontend targets only Go.

| You.com PR file | AnySearch correspondence on this baseline |
| --- | --- |
| `internal/service/web_search_provider.go` | Provider/key resolution and shared dispatcher registration; adapter split into `web_search_anysearch.go` |
| `internal/service/web_search_provider_test.go` | New adjacent `web_search_anysearch_test.go`; existing common provider tests retained |
| `rag/utils/web_search_conn.py` | Removed upstream; shared Go dispatcher owns this behavior |
| `rag/utils/youcom_conn.py` | Removed upstream; new Go AnySearch adapter owns this behavior |
| `test/unit_test/rag/utils/test_web_search_conn.py` | Removed upstream; Go key and dispatch tests cover the corresponding behavior |
| `test/unit_test/rag/utils/test_youcom_conn.py` | Removed upstream; Go httptest transport/envelope/content/citation tests cover it |
| `test/testcases/test_http_api/test_session_management/test_session_sdk_routes_unit.py` | Removed upstream; no Python SDK route extension on the Go baseline |
| `web/src/assets/svg/youcom.svg` | New `web/src/assets/svg/anysearch.svg`, a custom integration glyph, not an official brand mark |
| `web/src/components/web-search-form-field.tsx` | Catalog/key field/help link, optional tag and extraction controls |
| `web/src/constants/chat.ts` | `WebSearchProvider.AnySearch`; no keyless exception |
| `web/src/interfaces/database/chat.ts` | Key, tag, parameter object, extraction boolean |
| `web/src/locales/en.ts` | English configuration and quota descriptions |
| `web/src/locales/zh.ts` | Chinese configuration and quota descriptions |
| `web/src/pages/next-chats/chat/app-settings/use-chat-setting-schema.tsx` | Enum/options schema plus existing save-time required-key validation |
| `web/src/pages/next-chats/chat/use-show-internet.ts` | Current hook delegates to `hasWebSearchProvider`; requires no new branch |
| `web/src/pages/next-chats/chat/use-show-internet.test.ts` | Removed upstream; current `web-search-api-key.test.ts` covers Internet availability and required-key behavior |
| `web/src/pages/next-chats/chat/web-search-api-key.ts` | Explicit own-key mapping; existing keyless list remains unchanged |
| `docs/references/http_api_reference.md` | Full current provider list and AnySearch configuration fields |

Also inspected the open [SerpApi PR #20449](https://github.com/infiniflow/ragflow/pull/20449)
(head `3a5ea0e68c78432276d6923e7d99195770ca2bab`) and
[Search1API PR #20561](https://github.com/infiniflow/ragflow/pull/20561)
(head `20ed0747ab8267c6f9305b212f5c81f4e81cc241`). Their additions are not
included. Potential merge overlaps are the provider constants, resolver,
dispatcher, selector, key mapping, schema, locales, and API reference.
AnySearch's adapter/tests are separate files; preserve all provider entries if
those PRs merge before this integration is reviewed.

## Tests and build

Mock tests make no external API calls. They cover own-key enforcement, trimming,
opt-in selection, advanced option validation, Bearer/JSON payloads, content and
snippet handling, malformed envelopes, URLs, six-result cap, HTTP failures,
cancellation, response-size limits, transport redaction, extraction opt-in and
fallback, repeated discovery parameters, and shared Chat/Deep Research dispatch.
Frontend key helper tests cover required/blank keys and cross-provider isolation.

Run normal checks in a provisioned checkout:

```sh
bash build.sh --test -run 'AnySearch|WebSearch' ./internal/service/...
cd web
npm ci
npm exec jest -- src/pages/next-chats/chat/web-search-api-key.test.ts src/pages/next-chats/chat/app-settings/use-chat-setting-schema.test.ts --runInBand
npm run lint
npm run type-check
npm run build
```

`build.sh` requires the Linux Go toolchain and prepared native dependencies.
The dependency downloader also provisions large models; it must not be run
without considering the user's download constraints. An isolated compile of
the real provider sources with clearly disclosed tokenizer/service type
stand-ins is useful for adapter validation but is not a full backend build.
Local command outcomes and limitations are recorded in the local `PR-DRAFT.md`.

### Optional live check and billing gate

[Official authentication documentation](https://anysearch.com/docs/auth) states
that authenticated requests use the key's paid quota. Discovery consumes
neither paid nor free search quota. A key's existence does not prove a free
search allowance, and the script's flag is an operator attestation, not a
billing lookup. Confirm free granted quota and disabled paid billing outside
the script before performing search or extraction.

With a key already securely configured in `ANYSEARCH_API_KEY`:

```sh
node scripts/anysearch-live.mjs --operation sub-domains
# Only after independently confirming this key's zero-cost search quota:
node scripts/anysearch-live.mjs --confirm-free-quota
```

The script makes one request, never auto-loads `.env` files, refuses a missing
key, does not retry or fall back to anonymous search, and prints only safe
status/count metadata. Extraction is separately opt-in through
`--operation extract --confirm-free-quota`. Never mistake discovery success
for authenticated search/extract verification.

## Maintenance and acceptance

Track official endpoint/envelope/tag changes and refresh mocks only from
verified response shapes. Preserve required-key enforcement and safe error
handling, and rerun provider and key helper tests after dispatcher changes.
Keep extraction opt-in and its additional quota use visible in both locales.
Do not enable paid CI, new account authorizations, or automatic API retries.

For integration/bounty review, provide code, configuration instructions,
architecture, request/response mappings, mock results, build outcomes, and an
honest live-test status. This AI-assisted implementation still requires
Harrison's review and testing confirmation; no human review, bounty acceptance,
or upstream merge is claimed. Only the explicitly authorized fork branch may
be pushed. Do not create PRs, issues, or comments; keep the PR draft local.
