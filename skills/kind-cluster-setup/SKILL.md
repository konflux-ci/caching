---
name: kind-cluster-setup
description: Gotchas when setting up or tearing down the local kind cluster
---

# Managing Kind Clusters

- `kind load` calls `podman save` under the hood -- if it hangs, check the Podman socket (`systemctl --user status podman.socket`)
- `mage clean` also deletes squid and test images, not just the cluster -- re-running `mage all` afterwards rebuilds from scratch
- `mage kind:upClean` destroys deployed workloads and persistent volumes -- use `mage kind:up` if you only need to re-export kubeconfig
