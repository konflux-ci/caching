---
name: editing-helm-templates
description: Gotchas when editing Helm chart templates
---

# Editing Helm Templates

- `squid/templates/deployment.yaml` creates a **StatefulSet**, not a Deployment
- When `perSiteExporter.enabled=true`, liveness/readiness probes switch to **HTTPS 9302** (not TCP 3128)
