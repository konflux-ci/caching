---
name: kind-cluster-setup
description: Gotchas when setting up or tearing down the local kind cluster
---

# Managing Kind Clusters

- Image loading uses `podman save | podman exec <node> ctr images import` to bypass Kind v0.29.0's broken `--all-platforms` flag -- if it hangs, check the Podman socket (`systemctl --user status podman.socket`)
- `mage clean` also deletes squid and test images, not just the cluster -- re-running `mage all` afterwards rebuilds from scratch
- `mage kind:upClean` destroys deployed workloads and persistent volumes -- use `mage kind:up` if you only need to re-export kubeconfig
