# Historical Flash Next profile measurements

These are real retained measurements on two Radeon AI PRO R9700 cards. They compare **engine/model profiles**, with different weight formats, cache settings and speculation. They do not establish an engine-only speed ranking or qualify this PR on a GPU. AJEAN can connect to either server; connection support is independent of these speed observations.

Values are aggregate output tokens divided by client group wall time, **including prefill and queueing**, for the common-source ~30K workload. C means submitted clients. It does not mean native parallel execution: Strata has one active sequence, so C2/4/8 queue. Pure native prefill/decode rates were not emitted reliably for these selected cells and remain N/D.

| Actual model | Engine/profile | C1 | C2 | C4 | C8 |
|---|---|---:|---:|---:|---:|
| Qwen3.8 Flash Next | Paiton/vLLM | 77.47 | 136.62 | 137.65 | 164.85 |
| Qwen3.8 Flash Next | Radiance | 84.77 | 281.92 | 454.35 | 818.49 |
| Qwen3.8 Flash Next | Strata AMD | 34.83 | 61.43 | 59.27 | 56.14 |
| Swift1.5 Qwen3.8 Flash Next GSQ RCO | Paiton/vLLM | N/D | N/D | N/D | N/D |
| Swift1.5 Qwen3.8 Flash Next GSQ RCO | Radiance | 87.33 | 319.23 | 581.94 | 1025.36 |
| Swift1.5 Qwen3.8 Flash Next GSQ RCO | Strata AMD | 44.21 | 74.46 | 76.54 | 76.88 |

All rates are tok/s; none is a per-user decode speed.

- **Radiance, 17 October:** IQ4R experts/INT8 trunk, FP8 KV, MTP 3, context 262144, output budget 4096; median of 2 fresh-engine repetitions, natural prefix reuse.
- **Strata, 16 October:** Qwen UD-IQ4_XS and Swift IQ3_XXS, FP16 KV/resident 32768, MTP 3, context 131072, output budget 4096; one retained grid, Qwen with retained/imported cells. Different quantization and queued concurrency matter.
- **Paiton Qwen, 12 October:** FNQ / W3A4, TP2, native KV configuration auto, MTP 0, context 98304, prefix cache off; input 30622 tokens, generated3397–4096 tokens. C1 is the median of three first/repeat/different groups; C2/4/8 each one group. No reasoning output observed; it is not a matched low-effort run.
- **Paiton Swift:** no qualified matching Swift Flash Next result was found in these retained artifacts. Swift 27B results/failures concern another model and are not substituted.

The latest separate Radiance reproducibility campaign at ~128K/low/MTP 3 has a different protocol. These Strata recipes cannot fit that prompt plus 4096 reserved output (127685+4096 exceeds131072); Paiton's retained98K profile cannot represent it either. Compare those results separately, with their own repetition counts.

The compact [JSON](amd-engine-history.json) records raw-artifact SHA256 provenance and all conditions; [CSV](amd-engine-history.csv) contains the six-row matrix. Raw prompts, generated answers, private paths, server addresses and credentials are deliberately not included. N/D means absent or non-comparable evidence, not zero speed.
