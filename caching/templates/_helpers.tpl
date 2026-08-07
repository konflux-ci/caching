{{/*
=============================================================================
CHART-LEVEL HELPERS
=============================================================================
*/}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "caching.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Get current environment - defaults to "release" if not set
*/}}
{{- define "caching.environment" -}}
{{- .Values.environment | default "release" -}}
{{- end }}

{{/*
=============================================================================
NAMESPACE HELPERS
=============================================================================
*/}}

{{/*
Internal helper for namespace resolution with fallback chain
Returns component-specific namespace if set, falls back to namespace.name, defaults to "caching"
*/}}
{{- define "caching.component.namespace" -}}
{{- $componentNs := .componentNs -}}
{{- if $componentNs -}}
  {{- $componentNs -}}
{{- else if .ctx.Values.namespace.name -}}
  {{- .ctx.Values.namespace.name -}}
{{- else -}}
  caching
{{- end -}}
{{- end }}

{{/*
Determine namespace for Squid component
*/}}
{{- define "caching.squid.namespace" -}}
{{- include "caching.component.namespace" (dict "componentNs" .Values.squid.namespace "ctx" .) -}}
{{- end }}

{{/*
Determine namespace for Nginx component
*/}}
{{- define "caching.nginx.namespace" -}}
{{- include "caching.component.namespace" (dict "componentNs" .Values.nginx.namespace "ctx" .) -}}
{{- end }}

{{/*
=============================================================================
DNS / FQDN HELPERS
=============================================================================
*/}}

{{/*
Cluster DNS domain, defaulting to "cluster.local" when not set.
*/}}
{{- define "caching.clusterDomain" -}}
{{- .Values.clusterDomain | default "cluster.local" | trimSuffix "." -}}
{{- end }}

{{/*
Build a fully qualified domain name: <name>.<namespace>.svc.<clusterDomain>
*/}}
{{- define "caching.fqdn" -}}
{{- printf "%s.%s.svc.%s" .name .namespace (include "caching.clusterDomain" .ctx) -}}
{{- end }}

{{- define "caching.squid.fqdn" -}}
{{- include "caching.fqdn" (dict "name" .Values.squid.name "namespace" (include "caching.squid.namespace" .) "ctx" .) -}}
{{- end }}

{{- define "caching.nginx.fqdn" -}}
{{- include "caching.fqdn" (dict "name" .Values.nginx.name "namespace" (include "caching.nginx.namespace" .) "ctx" .) -}}
{{- end }}

{{- define "caching.test-server.fqdn" -}}
{{- include "caching.fqdn" (dict "name" "test-server" "namespace" (include "caching.squid.namespace" .) "ctx" .) -}}
{{- end }}

{{/*
=============================================================================
SQUID COMPONENT HELPERS
=============================================================================
*/}}

{{/*
Common labels for Squid component
*/}}
{{- define "caching.squid.labels" -}}
helm.sh/chart: {{ include "caching.chart" . }}
{{ include "caching.squid.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels for Squid component
NOTE: Values remain 'squid' - these are runtime identifiers, not chart names
*/}}
{{- define "caching.squid.selectorLabels" -}}
app.kubernetes.io/name: {{ .Values.squid.name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: squid-caching
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "caching.squid.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default .Values.squid.name .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Get squid image for current environment
*/}}
{{- define "caching.squid.image" -}}
{{- $env := include "caching.environment" . -}}
{{- $envSettings := index .Values.envSettings $env -}}
{{- if hasPrefix "sha256:" $envSettings.squid.image.tag -}}
  {{- printf "%s@%s" $envSettings.squid.image.repository $envSettings.squid.image.tag -}}
{{- else -}}
  {{- printf "%s:%s" $envSettings.squid.image.repository $envSettings.squid.image.tag -}}
{{- end -}}
{{- end }}

{{/*
Get test image for current environment
*/}}
{{- define "caching.test.image" -}}
{{- $env := include "caching.environment" . -}}
{{- $envSettings := index .Values.envSettings $env -}}
{{- if hasPrefix "sha256:" $envSettings.test.image.tag -}}
  {{- printf "%s@%s" $envSettings.test.image.repository $envSettings.test.image.tag -}}
{{- else -}}
  {{- printf "%s:%s" $envSettings.test.image.repository $envSettings.test.image.tag -}}
{{- end -}}
{{- end }}

{{/*
Default affinity rules for Squid when none are specified
*/}}
{{- define "caching.squid.defaultAffinity" -}}
podAntiAffinity:
  preferredDuringSchedulingIgnoredDuringExecution:
  - weight: 100
    podAffinityTerm:
      labelSelector:
        matchLabels:
          {{- include "caching.squid.selectorLabels" . | nindent 10 }}
      topologyKey: kubernetes.io/hostname
{{- end }}

{{/*
=============================================================================
NGINX COMPONENT HELPERS
=============================================================================
*/}}

{{/*
Common labels for Nginx component
*/}}
{{- define "caching.nginx.labels" -}}
helm.sh/chart: {{ include "caching.chart" . }}
{{ include "caching.nginx.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels for Nginx component
NOTE: Values remain 'nginx' - these are runtime identifiers, not chart names
*/}}
{{- define "caching.nginx.selectorLabels" -}}
app.kubernetes.io/name: {{ .Values.nginx.name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: nginx-caching
{{- end }}

{{/*
Default affinity rules for Nginx when none are specified
*/}}
{{- define "caching.nginx.defaultAffinity" -}}
podAntiAffinity:
  preferredDuringSchedulingIgnoredDuringExecution:
  - weight: 100
    podAffinityTerm:
      labelSelector:
        matchLabels:
          {{- include "caching.nginx.selectorLabels" . | nindent 10 }}
      topologyKey: kubernetes.io/hostname
{{- end }}

{{/*
Get nginx exporter image for current environment
*/}}
{{- define "caching.nginx.exporter.image" -}}
{{- $env := include "caching.environment" . -}}
{{- $envSettings := index .Values.envSettings $env -}}
{{- if hasPrefix "sha256:" $envSettings.nginx.exporter.image.tag -}}
  {{- printf "%s@%s" $envSettings.nginx.exporter.image.repository $envSettings.nginx.exporter.image.tag -}}
{{- else -}}
  {{- printf "%s:%s" $envSettings.nginx.exporter.image.repository $envSettings.nginx.exporter.image.tag -}}
{{- end -}}
{{- end }}
