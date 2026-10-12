# The release stamp

A published binary carries the buildhost release it shipped as. The stamp is a small trailer appended to the binary. After the build and before the upload. buildhost stores the bytes it is given and never changes a binary.

## Where it is applied

The action's "Stamp the release into each binary" step runs `.github/scripts/release-stamp.ts` on the build directory, just before the auto-release step:

- It finds the binaries the publish uploads, by the same rules as `buildhost-publish`: every `<binary>_<os>_<arch>` file, and every `kind: binary` entry of `buildhost-artifacts.json`.
- It names their project the way the publish does: `<repo>` for a binary named after the repo, else `<repo>/<binary>`. Binaries for more than one project fail the step, because the publish takes one release number.
- It creates that project's release over the job's OIDC token, with the branch, the commit, `create_service` and `apt-depends`. An explicit `autorelease_args` version is created as given, or used as it is when it exists.
- It appends the stamp to each binary in place, rewrites `checksums.txt`, and hands the number to the publish as `version`. A file that already carries a stamp gets its stamp replaced, not a second one.

The number in a stamp is therefore always the number of the release that holds the file, in that project. A failure anywhere fails the step, and nothing is published unstamped. A build outside the action has no stamp.

`dats/release-stamp.dats` runs `.github/scripts/release-stamp.test.ts` against a fake buildhost, including that a stamped ELF still runs.

## The format

The trailer is the last bytes of the file:

```
<payload: UTF-8 JSON> <length of payload: uint32, big-endian> <magic: "BUILDHOST-STAMP1", 16 ASCII bytes>
```

The payload is one JSON object, every field a non-empty string:

```json
{"project":"webhooks/webhook-runner","release":"347","revision":"9a245c29baf2040eda91efa39b87662a49a66aeb"}
```

## Reading it

Read the length field and the magic from the end of the file, as the reader below does. A file whose last bytes are not the magic has no stamp. Otherwise the length field before the magic gives the payload size, and the payload sits before the length field.

```go
func readStamp(f *os.File) (map[string]string, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	tail := make([]byte, 20)
	if st.Size() < 20 {
		return nil, nil
	}
	if _, err := f.ReadAt(tail, st.Size()-20); err != nil {
		return nil, err
	}
	if string(tail[4:]) != "BUILDHOST-STAMP1" {
		return nil, nil
	}
	n := int64(binary.BigEndian.Uint32(tail[:4]))
	if n > st.Size()-20 {
		return nil, fmt.Errorf("stamp claims %d payload bytes, more than the file holds", n)
	}
	payload := make([]byte, n)
	if _, err := f.ReadAt(payload, st.Size()-20-n); err != nil {
		return nil, err
	}
	var s map[string]string
	return s, json.Unmarshal(payload, &s)
}
```

A program reads its own stamp by opening `os.Executable()`.
