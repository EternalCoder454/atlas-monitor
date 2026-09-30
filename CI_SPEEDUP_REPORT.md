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

### Baseline on this branch

Runs dispatched on `ci-speedup` at b0921ca (workflows unchanged from beta).
A branch can only restore caches saved by itself or by `main`, so these are the
fair comparison for runs on this branch **[evidence, measured]**:

| Pass | CI run | Windows run | Total |
|---|---|---|---|
| base1 (36784421454 / 36784421782) | 95 s | 230 s | 325 s |
| base2 (36784836448 / 36784836092) | 105 s | 247 s | 352 s |
| **Median** | **100 s** | **238.5 s** | **338.5 s** |

Every cache hit in both passes (the Go build caches and setup-msys2's package
cache) **[evidence, from the logs]**.

## 1. What happened

| # | Commit | File | Change | Time before → after |
|---|---|---|---|---|
| 1 | 95a9424 | ci.yml, windows.yml | `timeout-minutes` on all three jobs: 60 for both Linux jobs, 90 for Windows. That is about twice the slowest successful cold run (31 and 40 minutes **[evidence, measured]**). The default is 360. | No change expected on a passing run **[inference]**. **Unverified**: no run could be started after it was pushed. |
| 2 | 3025442 | ci.yml, windows.yml | `paths-ignore` on push and pull_request: README.md, CLAUDE.md, `screenshots/**`, `images/**`. | A docs-only push goes from about 338 s to 0 s. That was only 3 of 147 recent commits **[evidence, git log]**, so the typical saving is small. **Unverified**: dispatch ignores path filters, so only a real docs-only push can show it. |
| 3 | 5537b5c | ci.yml | "Install the cache tools" installs tar and zstd only if the image lacks them. The log shows both "already installed" and dnf spending 7 s loading metadata to do nothing **[evidence, run 36784421454]**. | Expected about 7 s off each Linux job, so about 7 s off the CI run **[inference]**. dnf may simply load that metadata one step later instead **[speculation]**. **Unverified**: not run. |

All three pass `actionlint` **[evidence]**. Parsing the YAML shows the triggers
and branch lists are unchanged apart from the added `paths-ignore`
**[evidence]**.

## 2. What it means

Measured time saved so far: **none confirmed**. No run of changes 1–3 could be
made. Every figure above is the baseline.

- Estimated, per ordinary push: about 7 s off 338.5 s (2%), all from change 3
  **[inference]**.
- Estimated, per docs-only push: the whole 338.5 s **[inference]**.

The wall-clock wait for a push is set by Windows (238.5 s), and none of these
changes touch that path.

## 3. What is risky

- **Change 1 (timeouts):** a cold compile on a slow runner that took more than
  twice the worst run seen so far would now fail instead of finishing. The
  margins are 2× the measured worst case.
- **Change 2 (path filters):**
  - A docs-only push to `main` produces no CI or Windows run on main, so no
    fresh cache is saved there. Release builds restore main's caches, but the
    cache keys don't depend on docs, so the previous save still matches
    **[inference]**.
  - README.md is copied into the release tarball and Windows zip, so deleting
    it would break packaging, and a docs-only push would not catch that. The
    release workflow still would.
  - No branch protection or rulesets exist **[evidence: API 404 and `[]`]**,
    so a skipped workflow can't leave a required check pending.
- **Change 3 (tar/zstd):** if a future Fedora image lacks either tool, the
  `if` installs it exactly as before. The only behaviour difference is when
  both are already present.

## 4. Skipped items

From the list:
- **push and pull_request both running**: doesn't apply. Work is pushed
  straight to beta, and the last PR was #8 on 2026-09-24 **[evidence]**.
- **concurrency with cancel-in-progress on PRs**: already present on both
  workflows **[evidence, YAML]**.
  - Note: it also cancels superseded runs on `main` pushes (two cancelled
    main runs on 2026-09-30).
  - Your rules say never to cancel on main. I did not change it, because
    restricting it to PRs is a behaviour change for you to choose:
    `cancel-in-progress: ${{ github.event_name == 'pull_request' }}`.
- **Fast checks first**: already so. gofmt runs before vet, build and test;
  the Windows job builds the non-cgo packages first.
- **setup actions that cache**: nothing to swap. Linux uses Fedora's own
  packages in a container (with a dnf cache); Windows uses setup-msys2, which
  already caches packages.
- **Cache build output**: the Go build caches already exist in all three jobs.
- **Parallel jobs**: check and race already run in parallel. Splitting the
  Windows job would make each part pay the 110–120 s MSYS2 setup again.
- **matrix, trimmed PR matrix, nightly split, Docker, QEMU**: no matrix, no
  Docker and no slow integration suite in this repo.

Beyond the list (you allowed my suggestions):
- **Caching the installed MSYS2 tree**, which would be most of the 104 s
  unpack: skipped for correctness.
  - setup-msys2 has this built in and keeps it switched off
    (`INSTALL_CACHE_ENABLED = false`) because restored trees lost files, such
    as a missing header found by `pacman -Qkq` (msys2/setup-msys2#61)
    **[evidence]**.
  - A silently missing header could change a build.
- **Turning off Defender real-time scanning on the Windows runner** before the
  MSYS2 unpack: not done.
  - My tool permissions blocked it as a security weakening. After that,
    dispatching workflow runs was blocked too, which ended measurement.
  - This needs your call. Whether it would help is untested **[speculation]**.
- **Linking the GUI and console Windows executables in parallel** (about 45 s
  of two sequential links **[evidence, log]**): skipped.
  - It is in `packaging/stage-windows.sh`, which the release job runs, and
    release jobs are off limits.
- **`update: false` or `release: false` on setup-msys2**: skipped.
  - Each saves 8–14 s **[evidence, log]**.
  - But CI would then build against different MSYS2 packages from the release
    job, or against the runner's preinstalled MSYS2.
- **Larger or self-hosted runners**: suggestion only.
  - The Windows job is dominated by disk writes (package unpack) and a
    two-core link.
  - A larger Windows runner, or a self-hosted one with MSYS2 preinstalled,
    would cut the 110–120 s setup to near zero **[inference]**. That is not
    free, so not done.

## Final status

**Stop reason:** none of the four stop conditions fits exactly. Measurement was
blocked: after the Defender change was refused, starting workflow runs was
refused too, so no change could be verified.

- The list is otherwise used up: every other item either already applies or
  doesn't fit this repo (see section 4). That is closest to stop condition 2,
  "Nothing left".
- Starting total: **338.5 s** (median of 2 measured runs per workflow on this
  branch).
- Ending total: **not measured**. Estimated at about **331 s** (−2%)
  **[inference]**.

## 5. How to verify

```bash
gh workflow run ci.yml --repo EternalCoder454/atlas-monitor --ref ci-speedup
gh workflow run windows.yml --repo EternalCoder454/atlas-monitor --ref ci-speedup
gh run list --repo EternalCoder454/atlas-monitor --branch ci-speedup --limit 10
gh run view <run id> --repo EternalCoder454/atlas-monitor --json jobs
```

Wait for one run to finish before dispatching the same workflow again. Both
workflows set `cancel-in-progress`, so a second dispatch cancels the first.
