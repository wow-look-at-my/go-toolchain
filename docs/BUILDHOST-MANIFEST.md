# buildhost-artifacts.json — publishing one binary that runs on many platforms

## The problem it solves

buildhost's publish action discovers artifacts by filename, matching
`<binary>_<os>_<arch>[.exe]`. That grammar names exactly one platform per file,
so a binary that runs on three of them has no way to say so.

The old workaround was to copy the APE onto each per-platform name. The publish
action groups files by SHA-256, so the bytes only ever crossed the wire once —
but each name still became its own artifact row, with its own download link.
The registry showed the same binary three times, and no page ever said the three
were one file.

A filename cannot carry the set either: the regex components are `[a-z]+` and
`[a-z0-9]+`, so `linux/amd64+darwin/arm64` has nowhere to go.

## The contract

go-toolchain writes `buildhost-artifacts.json` at the root of the directory it
publishes (`build/`). buildhost-publish reads it, removes every listed file from
the filename scan, and uploads each as ONE artifact row carrying its platform
set. Files not listed keep the per-platform behavior unchanged.

```json
{
  "schema": 1,
  "artifacts": [
    {
      "file": "go-toolchain",
      "platforms": ["linux/amd64", "darwin/arm64", "windows/amd64"],
      "filename": "go-toolchain"
    }
  ]
}
```

- `schema` — must be 1. buildhost fails the publish on any other value rather
  than ignoring the file.
- `file` — path relative to the published directory. Must exist;
  `apeManifestEntries` checks before writing, so a broken manifest is caught
  where the artifact names are still in hand.
- `platforms` — `os/arch` pairs, at least one. The FIRST is the row's canonical
  slot: what appears in the row's os/arch columns, and what `dl` canonicalizes
  every covered platform's redirect to. So all three platforms resolve to one
  identical `static` URL, one digest, one ETag.
- `filename` — what the download is served as. Without it a consumer would
  receive a file called `go-toolchain`.

## Two fields that are deliberately absent

**`kind`.** Optional, and buildhost defaults it to `binary`. It selects
repackaging (apt/brew/npm/oci), not file format — APE-ness is a property
buildhost detects from the bytes and stores separately. Sending `"ape"` there
would be a category error.

**A display label.** The `APE:<platforms>` badge renders from the set buildhost
stores plus the format it detected. There is no field for a caller-supplied
label, because a label can disagree with the stored set, and then the badge
lies about where the binary runs.

## Server side

`PUT /api/v1/projects/{project}/releases/{version}/artifacts/ape?platforms=<os/arch,...>`

One request, one blob, one row. The literal `ape` segment replaces the
`{os}/{arch}` pair, which is why buildhost's `os=cosmo` rejection — a rule of
the per-platform grammar — cannot fire on this path. The server 400s if the
bytes are not an APE (`MZqFpD` at offset 0) and `platforms` names more than one
platform, and 409s if any named platform is already taken in that release.

## Producing it

`src/cmd/apemanifest.go`. Written whenever a cosmo APE was built — it is the
only way the APE publishes. So there is no build that produces one without the
other. `checksums.txt` lists the APE once, under its real filename; buildhost
skips `checksums.txt` and never reads it.
