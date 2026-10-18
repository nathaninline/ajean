# AMD engines: Radiance, Paiton and Strata HIP

**English** · [Français](moteurs-amd.fr.md)

AJEAN is the application: chat, Fast/Project/Jean/Base modes, history, memory,
tools and compaction. An **engine** executes model weights. **Qwen** and
**Swift** identify models or model variants; they are not engine names. A recipe
combines an exact model revision, weight format, engine build, hardware topology
and compatible settings.

This draft adds a **local Strata HIP import**. It does not add three managed AMD
engines. Radiance and Paiton can use AJEAN's existing External API transport when
their configured server provides a compatible chat API. The three engine names
shown in a development deployment are not a three-engine implementation in this
PR.

## Engine architecture and integration status

| Engine | What runs inference | Integration in this PR | Evidence and remaining work |
| --- | --- | --- | --- |
| **Radiance** | The standalone AMD inference engine published by StillDeadcode. | Uses the pre-existing External API preset path; no Radiance installer, managed launch configuration or engine-specific settings UI is added. | Radiance is the active AMD engine in a separate development deployment. That observation is not GPU qualification of this PR, and its deployment code/configuration is not included here. |
| **Paiton** | For the language-model path discussed here, Paiton runtime/plugin components work with a matching vLLM environment, which serves the chat API. | A compatible running server can be addressed through the existing External API path. No managed Paiton adapter or install recipe is added or qualified by this PR. | Use the model's pinned runtime and weight requirements. Historical measurements from a separate setup do not validate a new AJEAN installation. |
| **Strata HIP** | Strata's explicit AMD HIP backend, with its own executable, server configuration, tokenizer and compatible model assets. | New pinned local import/bundle installation, preflight, inactive preset preparation, declared setting variants and existing AJEAN process handoff. | These integration paths have CPU fixture tests. Real GPU execution, quality, performance and concurrency qualification remain pending for the imported recipe. |

The [Radiance publisher's page](https://hub.docker.com/r/stilldeadcode/radiance)
identifies the standalone engine and links its
[source repository](https://codeberg.org/StillDeadcode/radiance). Do not infer
that it is Strata, or assume that an older image named `vllm-radiance` has the
same runtime contract. No code lineage between these projects is claimed here.

The [Paiton project](https://github.com/Eliovp-BV/paiton-vllm-plugin) documents
its language-model serving path, model-specific presets and matching vLLM
requirements. [Strata's AMD guide](https://github.com/Niko1221/Strata/blob/main/docs/AMD_HIP.md)
describes its HIP backend. These upstream projects evolve independently; pin
the versions actually used rather than treating current documentation as a
qualification receipt.

## API connection and local engine management are different operations

AJEAN already supports `EXTERNAL=1`, `EXTERNAL_URL`, `EXTERNAL_MODEL` and an
optional `EXTERNAL_KEY` in an External API preset. This reuses the original chat
pipeline; see [the existing transport](../internal/ajean/backend_external.go).
A compatible HTTP interface does not install an engine, convert its weights or
prove that every sampling, reasoning, tool or vision option is supported.

The new `MOE_BACKEND=hip` branch instead imports a local **Strata** installation
into the existing MoE workflow; see the [import guide](amd-hip-import.md) and
[implementation](../internal/ajean/backend_moe_hip.go). It does not reinterpret
Radiance or Paiton assets as Strata assets. The model dialog prepares inactive
presets; explicit activation through the existing service/preset interfaces can
start the imported engine and must be treated separately from CPU validation.

## Settings belong to a verified recipe

- **Weight quantization:** a Radiance IQ4R recipe, a Paiton W3A4/MXFP4 recipe and
  a Strata GGUF/pack recipe are different assets. Similar bit counts do not make
  their files interchangeable.
- **Context:** use the engine's supported total window, including input and
  output. A 131072-token window does not fit approximately 128K input plus 4096
  output. This draft's Strata import is capped at 131072; that is not a limit
  imposed on another engine's External API preset.
- **KV cache, MTP/speculation and reasoning:** valid formats, draft/head assets,
  argument names and effort values depend on the exact engine/model build.
  Expose only declared supported choices. Do not translate an unsupported value
  into another value or copy another engine's settings silently.
- **Multiple GPUs and concurrency:** layer splitting, tensor parallelism and
  two independent instances describe different execution topologies. A queued
  request is not proof of simultaneous decoding.

## Extending the integration to all three engines

A shared engine catalogue can separate engine identity from model family and
recipe identity. Each engine adapter should declare verified identity/health,
capabilities, accepted settings and which installation/lifecycle operations it
actually owns. Unsupported operations should remain unavailable with a reason.
All adapters should reuse AJEAN's original chat, modes, memory and compactor.

That is further integration work, not code already delivered by this draft.
Qualification must identify the engine build, assets, AMD hardware, actual
input/output lengths and concurrency. CPU import tests, an API health response
and a historical benchmark are distinct evidence and must be reported as such.
