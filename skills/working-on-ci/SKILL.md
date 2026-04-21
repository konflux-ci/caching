---
name: working-on-ci
description: Gotchas when modifying CI/Tekton pipelines
---

# Working on CI

- PR images expire in **5 days** (`image-expires-after: 5d` in `.tekton/`)
- Push/main images **don't expire**
- Image promotion to `quay.io/konflux-ci/` is configured **externally** (not in this repo)
