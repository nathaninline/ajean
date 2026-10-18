# AMD engines: Radiance, Paiton and Strata HIP

**English** · [Français](moteurs-amd.fr.md)

AJEAN provides chat, Fast/Project/Jean/Base modes, history, memory, tools and compaction. An engine executes model weights. Qwen and Swift identify models or variants, not engines. A recipe combines an exact model revision, format, engine build, hardware topology and compatible settings.

This PR adds two concrete paths to the original workflow: **named Radiance/Paiton server connections** and **pinned local Strata HIP imports**. They have different lifecycle ownership.

| Engine | Inference runtime | Integration delivered | Lifecycle and qualification limits |
|---|---|---|---|
| Radiance | Standalone engine published by StillDeadcode. | Engine entry opens the original external preset editor; save named connections, discover advertised models without completion, explicitly select a checked connection for the original chat. | AJEAN does not install/reload the server or reconfigure GPU/KV/MTP/weights. An advertised ID is not proof of loaded weight identity. |
| Paiton | The language-model path uses Paiton runtime/plugin components with a matching vLLM environment. | Same named connection workflow with persisted URL/model/key, discovery and explicit selection. | Deployment, template/tool parser and runtime compatibility remain properties of the separately operated server. |
| Strata HIP | Explicit AMD HIP build with its executable, server configuration, tokenizer and compatible assets. | Pinned local import/bundle installation, preflight, inactive preset preparation, declared settings variants and original AJEAN process handoff. | Import paths have CPU fixture tests; each actual recipe still needs GPU execution, quality and performance qualification. |

Use [Radiance/Paiton connection instructions](amd-engine-connections.md) for the working server connection path, or [Strata HIP import instructions](amd-hip-import.md) for local engine installation. The existing automatic hardware detection/CUDA route remains unchanged even when a HIP catalogue exists; local HIP import mode is explicit.

## Configuration belongs to the actual server or recipe

Saved connections use the existing `EXTERNAL=1`, URL/model/key fields plus `EXTERNAL_ENGINE=radiance|paiton`. AJEAN checks `GET /v1/models`; saving does not activate a connection. Explicit selection refuses a missing advertised ID or an active generation/installation job, then applies the normal chat configuration without a service action. No fallback model is silently selected by AJEAN. The server's own interpretation of model names must still be checked.

Context and vision are operator declarations, not commands that change server settings. Radiance reasoning choices come from the selected model's declared capabilities; Server default omits the override. Its request uses top-level `reasoning_effort`; Paiton retains its existing request mapping. Weight quantization, KV precision, MTP/drafter assets, GPU placement and native concurrency remain in the actual recipe. Radiance `.rad`, Paiton checkpoints/native artifacts and Strata GGUF/packs are not interchangeable. A queued client is not proof of simultaneous native decoding.

The model dialog and chat remain original AJEAN interfaces. External connections do not introduce another chat server, proxy or engine installer. The Strata branch can start its imported engine only through explicit existing preset/service actions; import preparation is separate from activation.

## Sources and evidence

The [Radiance publisher](https://hub.docker.com/r/stilldeadcode/radiance) links the standalone [source](https://codeberg.org/StillDeadcode/radiance). Older images called vllm-radiance have a different contract; no code lineage with Strata is inferred. [Paiton](https://github.com/Eliovp-BV/paiton-vllm-plugin) documents model-specific runtimes and launchers. [Strata's AMD guide](https://github.com/Niko1221/Strata/blob/main/docs/AMD_HIP.md) describes its HIP backend. Pin the actual build and assets.

[Historical Flash Next measurements](benchmarks/README.md) contain a six-row engine/model matrix, exact conditions and sanitized raw-artifact hashes. They are measurements of separate server profiles, with different quantization/cache/speculation and repetition counts; they do not qualify this PR's connection code or establish an engine-only speed ranking. CPU fixtures, API discovery, tool correctness and GPU performance are distinct evidence.
