---
name: track-bugs
description: Record and look up bugs of doinspect. Every bug has a record in the specs repository (doinspect-app/bugs/, index bugs.md) with its cause, what did not work, the fix and how to spot it again. Use when the user says something is broken, missing, wrong, slow, "does not work", "I cannot see", "it used to", when a test fails for an unknown reason, when you find a defect while building something else, and before diagnosing any defect, to find an earlier bug with the same cause.
---

# Track bugs

The records are in `../raspberry-pi-apps-functional-specs/doinspect-app/`: one file per bug in `bugs/`, the index and the cause classes in `bugs.md`. A record is never deleted. The fix is the valuable part: the same cause comes back in another form.

## 1. Search first, before you open the code

Read `bugs.md`. Look for records that match:

- the **area** (the use case group, as listed in `spec/README.md`),
- the **cause class** you suspect (stale-page-state, layout-width, second-copy-out-of-step, ...),
- the **words** the user used, in the "How to spot it again" parts: `grep -ril "<word>" bugs/`.

Open the ones that look alike and read Cause, What did not work and Fix. Tell the user what you found, or that nothing like it is on record. The other projects keep their own records; doslim's (`../raspberry-pi-apps-functional-specs/doslim-app/bugs.md`) is the largest and worth a look for a cause that is not about one app.

## 2. Open the record

Next free number, file `bugs/B<NNN>-<short-name>.md`, a row in `bugs.md`. Header:

```
# B001 — <what the user sees, in one line>

Status: New
Reported: <who, when, their words in quotes, device and version>
Affects: [UC-…](../spec/…)
Cause class: <one from bugs.md, or a new one added to the table there>
Similar to: [B00n](B00n-….md), or "none found; searched for <what>"
Fixed by: not yet
```

Then the seven sections, in this order: `## Symptom`, `## How to reproduce`, `## Cause`, `## What did not work`, `## Fix`, `## How to spot it again`, `## Guard`. Fill Symptom and How to reproduce now. Write "Not known yet." where that is true; never leave a section empty and never guess a cause.

Add the bug to the `Bugs:` line of every use case it affects.

## 3. Find out what it is

Diagnose with the `diagnosing-bugs` skill when the cause is not plain. Three outcomes:

- **A bug.** Status `Confirmed`. Write the Cause with the file and the function. Add a task of type `fix` with `Implements:` its use case and `Fixes: B…`. Execute it only when the user says so.
- **Not a bug: decided.** It does what a decision says. Status `Not a bug: decided in D…`. If the user may want it changed, write a question in `questions.md`.
- **Not a bug: switched off or missing.** The feature exists behind a setting, or was never built. Status `Not a bug: …`, and name the task or question it led to.

One report can hold both a decision and a real bug. Give each its own record.

## 4. When it is fixed

Before you call it done, complete the record:

- **Cause**: the real reason, with file and function. Not the symptom again.
- **What did not work**: every approach that was tried and dropped, and why. "Nothing else was tried." when that is so.
- **Fix**: what changed, in which files, in which commit, and why this way.
- **How to spot it again**: the words a user would use, the signs in the app, where to look in the code, and what else shares the cause.
- **Guard**: the test that fails if it comes back, with its path. If there is none, say why, and say what was looked at by hand.
- `Status: Fixed in <version>`, `Fixed by:` the task. After the reporter confirms: `Confirmed fixed`.

Update the row in `bugs.md`. If the same cause class now has several records, say so to the user: a class that keeps growing needs a guard for the class, not one more fix.

## 5. Check

```bash
cd ../raspberry-pi-apps-functional-specs
python3 tools/speclint.py doinspect-app
python3 build-index.py
```

The lint fails on a record without its header lines or sections, on a cause class that is not in `bugs.md`, on a `fix` task without `Fixes:`, and when a use case's `Bugs:` line differs from the records.

## Rules

- A defect you find while building something else gets a record too, even when you fix it in the same task ("fixed on the way" is how causes get lost).
- Quote the user's report in their words.
- A failing test with an unknown reason is a record with status `Under investigation`; keep the log. If it was the machine and not the product, close it as `Not a bug` with the evidence.
- No real values: no domain, address, user name or path under a home folder.
