# CI speed-up report

Branch `ci-speedup`, cut from `beta` at f48e06d. Nothing here touches `main`,
`minimal`, `release.yml`, secrets or `permissions:` blocks.

Labels: **[evidence]** comes from run logs, the GitHub API or the YAML;
**[inference]** is reasoned from evidence; **[speculation]** is a guess.
Times are **measured** (from real runs) or **estimated**.

## Baseline

The gh CLI works, so every number here is from real runs.

"Total CI time" below is the sum of the two workflows' run times for one push:
CI (Linux) plus Windows. They run side by side, so the wall-clock wait for a
push to go green is the longer of the two, which is always Windows.

From beta's own pushes (successful runs, 2026-09-30) **[evidence, measured]**:

| Workflow | Runs | Median | Range |
|---|---|---|---|
| CI | 9 | 95 s | 87–144 s |
| Windows | 11 | 249 s | 207–269 s |
| Total (sum) | | 344 s | |

Slowest jobs (6 runs each) **[evidence, measured]**:

| Job | Median |
|---|---|
| Windows `exe` | 235 s |
| CI `build · vet · test` | 90 s |
| CI `race detector` | 72 s |

There are only 3 jobs. Release is not counted (off limits).

Slowest steps **[evidence, measured]**:

| Step | Median |
|---|---|
| Windows: `msys2/setup-msys2` | 120 s |
| Windows: Build and stage the distribution | 50 s |
| CI check: Test | 27 s |
| CI race: Test with -race | 24 s |
| Windows: Start it | 20 s (a fixed `sleep 20`) |

Inside `setup-msys2` (one run's log, 36783362343) **[evidence, measured]**:
- about 5 s to unpack the base system;
- 4 s to restore its package cache (288 MB, an exact hit);
- 8 s for the two `pacman -Syuu` passes;
- **104 s to install 155 packages (1.7 GB installed)**: 26 s checking, about
  60 s extracting, 17 s of post-install hooks.

Downloads are already cached, so what costs the time is unpacking and hooks on
NTFS.

Inside "Build and stage" **[evidence, measured]**: two full links of the gotk4
binary (the GUI exe and the console exe), then copying the DLL set and icons.

## 1. What happened

(changes, one per commit, below)

## 2. What it means

(total saved so far)

## 3. What is risky

(per change)

## 4. Skipped items

(see below)

## 5. How to verify

```bash
gh workflow run ci.yml --repo EternalCoder454/atlas-monitor --ref ci-speedup
gh workflow run windows.yml --repo EternalCoder454/atlas-monitor --ref ci-speedup
gh run list --repo EternalCoder454/atlas-monitor --branch ci-speedup --limit 10
gh run view <run id> --repo EternalCoder454/atlas-monitor --json jobs
```

Wait for one run to finish before dispatching the same workflow again. Both
workflows set `cancel-in-progress`, so a second dispatch cancels the first.
