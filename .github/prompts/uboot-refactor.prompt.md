---
mode: agent
tools: ['edit', 'runNotebooks', 'search', 'new', 'runCommands', 'runTasks', 'context7/*', 'memory/*', 'deepwiki/*', 'github/*', 'sequentialthinking/*', 'usages', 'vscodeAPI', 'think', 'problems', 'changes', 'testFailure', 'openSimpleBrowser', 'fetch', 'githubRepo', 'extensions', 'todos', 'runTests']
---
Let's instead stick to the existing structure. 

## Goal

Align our attempt to use u-boot as an embedded alternative to iPXE as closely as possible to the existing code in the upstream tinkerbell repo: https://github.com/tinkerbell/tinkerbell

The result should be a minimal implementation that properly leverages u-boot to mirror the behavior of the existing iPXE implementaiton - for scenarios like raspberry PI booting.

The only feature that should be entirely "additional" is the Hook http server. This should remain, but be refactored to not re-download on each restart.

## Phase 1

- [ ] Put the uboot script under `smee/internal/ipxe/binary/file`. 
- [ ] Create it with a simple shell script and add that to `.github/workflows/ipxe.yaml`
- [ ] Revert our TFTP based changes like everything in `smee/internal/firmware` and its usage as well as the workflow that creates it `.github/workflows/uboot.yaml`
- [ ] There will be no reason to serve TFTP firmware or OS images as we will use http to serve the images following the pattern for iPXE scripts
- [ ] Match the u-boot script as closely as possible to the usage of iPXE in the project
- [ ] Serve the u-boot script as the boot file when netboot is configured - like iPXE
- [ ] Ignore clients that have netboot disabled
- [ ] Remove the other features we've added like `smee/internal/tftp/hook`, `smee/internal/tftp/firmware`, and potentially `smee/internal/tftp/pxelinux`
- [ ] Analyze whether `smee/internal/tftp/pxelinux` is needed, and if so, move it under `smee/internal/ipxe/binary/tftp.go`
- [ ] Hook should exclusively be an http fileserver serving downloaded hook images

## Phase 2

- [ ] Make sure all resulting code has complete test coverage
- [ ] Verify that `golangci-lint run --fix` does not produce any errors
- [ ] Fix any errors resulting from golangci-lint
- [ ] Document the resulting changes
