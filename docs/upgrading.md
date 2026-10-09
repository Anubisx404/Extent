# Upgrading to v1.1.0

This guide covers upgrading an existing v1.0.x installation to v1.1.0.

## 1. Install the new binary

Install v1.1.0 the same way you installed v1.0.x (release archive or `go install`), then confirm the version:

```sh
extent version
```

## 2. Regenerate the stack configuration

v1.1.0 changes the generated LGTM stack configuration: tail sampling, Grafana credentials, and the cardinality processor. Regenerate the files in each repository that uses Extent:

```sh
extent apply --force /path/to/repo
```

Review the result with `git diff` before committing it.

## 3. Node ESM projects

If the project is a Node ESM project (`"type": "module"` in `package.json`), re-run the bootstrap instrumentation so the start script uses the `--import` form:

```sh
extent instrument --mode bootstrap --apply /path/to/repo
```

## 4. Undo still works

`extent instrument --undo` continues to work on changes written by v1.0.x. You do not need to undo and re-apply before upgrading.
