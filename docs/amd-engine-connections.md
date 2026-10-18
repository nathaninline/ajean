# Radiance and Paiton connections

AJEAN can save, discover and select named Radiance or Paiton connections in the original engine/model interface. They are ordinary External API presets: chat, the four modes, tools, history, encryption and the compactor keep the existing AJEAN pipeline. This is a working connection path to a server you operate separately. The local Strata HIP import/installer remains a separate backend.

## Connect an existing server

1. Start and configure your engine using its own supported deployment. Record its URL, advertised model ID, optional API key and actual context limit.
2. Open the Radiance or Paiton engine entry. Create a connection using the original external preset editor; enter URL, model, key if required, and declared context/vision support.
3. Test/discover models. For these named engines AJEAN sends only `GET /v1/models` (including when the entered base ends in `/v1`), with the optional Bearer key; it does not send a completion. Leave model empty for discovery, then choose an exact advertised ID.
4. Save. This writes an inactive preset; it does not select a model or control the server.
5. Explicitly select the saved connection. AJEAN checks the advertised model again, refuses an unknown ID or an active generation/installation job, and updates its normal chat configuration. It does not start, stop or reload a service. The normal preset switch and CLI switch use the same named-connection selection path.

The connection is not a hardware or quality qualification. `/models` is a declaration by the server: it does not prove weight identity, available context or correct tool execution. Radiance explicitly documents that a request naming any model may still be served by the loaded model; verify the operator's running recipe separately.

## Preset format

The existing preset format is extended by `EXTERNAL_ENGINE=radiance` or `paiton`:

```dotenv
EXTERNAL=1
EXTERNAL_ENGINE=radiance
EXTERNAL_URL=http://127.0.0.1:8000/v1
EXTERNAL_MODEL=replace-with-an-exact-advertised-id
CTX=131072
```

Use your server's real context, not the illustrative value. Paiton's Qwen3.8 **27B** guide uses `http://127.0.0.1:18982/v1`, model `Qwen3.8`; a Flash Next deployment can use another endpoint/model ID. Do not infer either from the engine label. Optional `EXTERNAL_KEY` stores the Bearer secret in the local preset; the named editor writes presets atomically with owner-only permissions and does not return saved keys in clear text. Editing without touching the key retains it. `EXTERNAL_VISION=1` is an operator declaration, not an automatic capability check.

Connection API: `GET /api/engine/connections`; existing `POST /api/preset/external/test` and `/save` accept `engine`, `name`, `url`, `model`, `key`, `ctx`, `vision`, optional `effort`, `id` and `keyTouched`; `POST /api/engine/connections/select` takes `{"id":"saved-preset-id"}`. A context in this request is a string. Listing exposes `hasKey`, never the key.

## Declared reasoning effort

The connection editor offers **Server default**. For Radiance, other choices come only from the selected model's `capabilities.reasoning_efforts` in the discovery response. Test/selection rejects an effort not advertised by that model; values are not silently aliased. A nonempty choice is saved as `REASONING_EFFORT` and sent as top-level `reasoning_effort`, without `chat_template_kwargs`. Empty means omit the override; omitting the optional API `effort` field on edit preserves the saved value.

Selection takes a capability snapshot for the original composer shortcut. Retest/reselect after changing the server recipe; this is not a live server settings editor. Radiance compaction requests `none` only when the snapshot advertises it, otherwise it leaves the server default. Paiton and generic/local connections retain their existing `chat_template_kwargs` behavior. These mappings do not prove reasoning quality or tool correctness.

## Engine requirements and limits

Both chat paths require OpenAI-compatible `POST /v1/chat/completions`, including streamed responses and any tool calls used by the chosen AJEAN mode. Radiance documents chat/tools/streaming and model discovery in its [official guide](https://codeberg.org/StillDeadcode/radiance/src/branch/main/docs/GUIDE.md). Paiton's language-model deployments use vLLM's API; use the specific model's [official setup guide](https://github.com/Eliovp-BV/paiton-vllm-plugin). Parser, chat template, reasoning, images and tool support remain properties of that exact server/model deployment. A successful discovery check does not qualify them.

Installation, downloads, engine upgrades, process ownership, model load/unload, GPU placement, server context/KV/MTP/quantization and concurrency are managed outside this connection feature. Capability flags report `install:false`, `reload:false`, `server_settings:false`. Declared AJEAN context and vision do not rewrite server settings. No GPU performance, simultaneous-user capacity or lifecycle qualification is inferred from an engine name or a CPU fixture. Pin and validate your engine image/build and weights separately.
