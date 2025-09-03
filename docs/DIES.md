Here’s a compact “field guide” you can keep for future-you.

# What happened (symptoms)

* In some dirs (notably under `~/Code`), your Go binary ran once or twice, then `./maestro -help` → **`zsh: killed`**.
* `spctl --assess -vv ./maestro` said **rejected**.
* `xattr -l` often showed **`com.apple.provenance`** on the dir/new files; clearing it sometimes didn’t stick.

# Why it happens (root causes we hit)

* **Gatekeeper/AMFI** blocks execution based on a mix of *signature* and *context* (where/how a file was created, parent process).
* **Extended attributes** (`com.apple.quarantine`, `com.apple.provenance`) can be applied to:

  * the **destination folder** (new files inherit it),
  * the **file you copy**, or
  * the **creating process** (your shell, via its launcher).
* On current macOS, seeing `com.apple.provenance` on **apps and new files is normal** and does **not** by itself block Terminal exec. The blocker was the **combination of context + unsigned binary**.

# What reliably fixes dev runs (works today)

1. **Copy without xattrs** and **ad-hoc sign** before running:

   ```bash
   cp -X ~/Code/maestro/bin/maestro ./maestro
   codesign --force --sign - --timestamp=none ./maestro
   ./maestro -help
   ```

   * This worked in `/tmp` and works in your project dirs; it sidesteps the flaky context.

2. Your updated `test.sh` now bakes this in:

   * `cp -X …` then `codesign -` before launching.

3. (Optional) If a dir repeatedly re-stamps files, you can **clean attributes**:

   * Our `cleanprov.sh` walks up to `$HOME` and removes `com.apple.quarantine`/`com.apple.provenance`.
   * Open a **fresh Terminal window** after cleaning to drop any “tainted” process context.

# Things we tried that are **no longer the way**

* `spctl --add` is **deprecated** on modern macOS → “operation is no longer supported.”
* Toggling Gatekeeper (`spctl --master-disable/enable`) was only used to prove the binary itself is fine.

# Quick triage checklist (next time it “just dies”)

1. **Binary OK?**

   ```bash
   mkdir /tmp/run-test && cd /tmp/run-test
   cp -X ~/Code/maestro/bin/maestro ./maestro
   codesign --force --sign - --timestamp=none ./maestro
   ./maestro -help
   ```

   If this runs: your build is good; the failure is environment/context.

2. **If a specific dir fails:**

   ```bash
   xattr -l . ..
   cleanprov.sh -v .
   exec /bin/zsh -l   # fresh shell in new window
   cp -X … && codesign - … && ./maestro -help
   ```

   (Seeing `com.apple.provenance` alone isn’t fatal; the `codesign -` is the key.)

3. **Avoid re-poisoning:**

   * In any script that stages the binary, always use **`cp -X`** (or `ditto --noextattr` / `rsync --no-xattrs`).
   * Don’t rely on `spctl --add`.

# Packaging for distribution (so users don’t hit this)

Ad-hoc signing is only for dev. For shipping:

1. **Sign with Developer ID (not ad-hoc)** and **Hardened Runtime**:

   ```bash
   codesign --force --options runtime --timestamp \
     --sign "Developer ID Application: Your Name (TEAMID)" \
     /path/to/maestro
   ```

2. **Notarize** with Apple and **staple**:

   ```bash
   # Zip first (keeps parent and avoids stray attrs)
   ditto -c -k --keepParent /path/to/maestro maestro.zip

   # Submit (set up notarytool once with your Apple ID/issuer/key)
   xcrun notarytool submit maestro.zip --keychain-profile "NotaryProfile" --wait

   # Staple & verify
   xcrun stapler staple /path/to/maestro
   spctl -a -vv -t execute /path/to/maestro
   ```

3. **Distribute** the stapled binary (or a DMG/ZIP you notarized). Users can run it without scary prompts or kills.

# Preventative habits

* Keep dev runs out of cloud-provider mounts; prefer a plain path under `$HOME`.
* Always stage with **no xattrs** and **ad-hoc sign** before running in arbitrary dirs.
* If you script logics that create/copy files, prefer `cp -X`/`ditto --noextattr`.
* Don’t worry if `xattr -l` shows `com.apple.provenance`; worry only if the app actually gets killed. Use `codesign -` in dev and proper **Developer ID + notarization** when distributing.

If you want, I can also drop in a tiny `stage-run` helper that always does `cp -X` + `codesign -` before executing so you never think about this again.

