# Releasing

A release is a tag; CI does the rest.

```sh
just release 0.2.0
```

The recipe refuses to run unless the tree is clean, `main` is checked out and in sync with
`origin/main`, and `just ci` passes. It then creates the annotated tag `v0.2.0` and pushes it.
`.github/workflows/release.yml` runs the tests again and GoReleaser builds `coachctl` for
linux/darwin × amd64/arm64, stamps the version (`coachctl --version`), and publishes the raw
binaries with `checksums.txt` as a GitHub release with generated notes. Watch it with
`gh run watch`; edit the notes afterwards when a release deserves narrative.

`just snapshot` builds every target locally into `dist/` without publishing.

## Versioning

Semver by intent: **major** for breaking changes to the command surface, exit codes, `--json`
shapes or the record's file formats; **minor** for features; **patch** for fixes. Until 1.0, minor
releases may still break, and the release notes say how to migrate.

## After a release

Users upgrade by replacing the binary and running `coachctl skill install`, so the skill matches
the commands. `coachctl doctor` warns when they do not.
