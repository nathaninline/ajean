# Importing a local Strata HIP installation (experimental)

This change adds an explicit HIP branch to AJEAN's existing MoE backend. It
reuses the original model dialog, presets, installer jobs, chat, four chat modes,
memory, encryption and compactor. It does not add another chat server or proxy.

**GPU qualification is pending.** CPU tests verify file integrity, transactions,
configuration and process handoff using fixtures. They do not establish AMD GPU
compatibility, throughput, MTP or INT8 quality, stability, or simultaneous users.
No model weights, engine binaries, Python environment or third-party sources are
included. Obtain compatible assets separately, preserving their attribution and
licenses, and qualify them before enabling a model.

## Scope

The initial branch targets Linux x86_64, text only, one active generation and
loopback binding. A nonempty operator HIP recipe or install catalogue explicitly
selects this branch before NVIDIA detection, including on mixed-GPU machines.
Without a HIP catalogue, the existing CUDA path is unchanged. This is not
hardware discovery: a catalogue declares an import, not a verified working GPU.

Imported engine arguments remain explicit. CUDA helpers, vision, remote/batched
execution and implicit device remapping are rejected. Context is capped at
131072. Editable context/KV/MTP settings are offered only by recipe metadata;
accepted parser settings still require GPU qualification for the imported build.

## Import an existing installation

Create `AJEAN_HOME/moe-hip/catalogue.json` in your own AJEAN profile. Paths below
are examples; replace every placeholder and digest with verified local values.
An engine config follows the existing Strata `serve/server.py --engine strata`
format and must explicitly declare `backend: "hip"`, `exe`, `cwd`, `tokenizer`,
`parallel: 1`, a string-array `args` with separate `--max-context VALUE`, and any
explicit `env`/`lib_dirs`. Its context must match the recipe.

```json
{
  "recipes": [{
    "id": "swift-iq3",
    "family": "swift",
    "label": "Swift Flash Next IQ3_XXS",
    "config": "/opt/ajean-hip/recipe/config.json",
    "source": "/opt/ajean-hip/source",
    "python": "/opt/ajean-hip/runtime/bin/python",
    "engine_sha256": "<64-hex-engine-sha256>",
    "context": 131072,
    "config_sha256": "<64-hex-config-sha256>",
    "source_manifest": "/opt/ajean-hip/source-manifest.json",
    "source_manifest_sha256": "<64-hex-source-manifest-sha256>"
  }]
}
```

The source manifest has `{"files":{"serve/server.py":"<sha256>", ...}}`.
Include every imported application source required by the pinned installation.
Preflight validates the config, engine digest, source manifest, tokenizer and
required assets; the source manifest does not pin Python, its packages, system
libraries or all weight bytes. Hash checks provide integrity, not a sandbox:
only an operator who trusts the imported runtime should create these catalogues.

Run the offline audit without starting a model:

```sh
AJEAN_HOME=/path/to/your/profile ajean moe-hip-check
```

Open the existing MoE model dialog. Swift/Classic segments, quantization cards
and settings use declared recipe metadata. Preparing/saving creates an inactive
preset or pinned variant; it never changes the active config or starts a model.
The modal's activation button remains unavailable pending qualification. The
normal AJEAN preset/service interfaces are still the existing interfaces: an
operator can explicitly activate a prepared preset there, which **does** start
the imported engine. Do not use those actions during CPU-only validation.

## Install from a local, pinned bundle

`AJEAN_HOME/moe-hip/install-catalogue.json` uses an `entries` array of
`{id,family,label,manifest,manifest_sha256}`. Each absolute manifest path names a
schema-1 document with `files`, optional `refs`, `config_template`, `source_dir`,
`python` and `context`. File items use absolute `source`, safe relative `target`,
`sha256` and optional mode; references additionally require `read_only: true`.
`${INSTALL_ROOT}` in the config template is expanded only within the new staged
installation. See `hipInstallFixture` in `backend_moe_hip_install_test.go` for a
complete executable CPU fixture of this format.

```sh
AJEAN_HOME=/path/to/your/profile ajean moe-hip-install swift-iq3
```

The installer copies/hashes local files, verifies referenced assets, commits a
complete installation, then publishes catalogue/preset metadata. It performs no
download, compilation, Python execution, activation or model generation. The
original installer job UI provides progress/cancellation. Large reference hashes
can be expensive; keep enough disk space for copied files. Exact retries are
idempotent; drift, cancellation and failed publication are tested.

## Validation

```sh
go test -race ./internal/ajean -run '^TestMoe' -count=1
go run ./tools/verify-i18n
go generate ./internal/ajean
go build ./cmd/ajean
```

The test suite uses temporary files and fixture executables, not a GPU runtime.
A real GPU qualification and visual regression checks on AMD and NVIDIA remain
separate requirements before calling this backend production-ready.
