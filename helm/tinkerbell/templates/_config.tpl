{{- /* Generate config map data */}}
{{- define "tinkerbell.configData" -}}
{{- $values := .Values.deployment.envs -}}
{{- range $kk, $vv := $values }}
{{- if kindIs "map" $vv }}
{{- range $k, $v := $vv }}
{{- $key := (list "TINKERBELL" (ternary "" (snakecase $kk | upper) (eq (upper $kk) "GLOBALS")) (snakecase $k | upper) | compact | join "_") }}
{{- if kindIs "invalid" $v }}
{{ $key }}:
{{- else if kindIs "map" $v }}
{{- if $v }}
{{ $key }}: {{ $v | toJson | quote }}
{{- else }}
{{ $key }}:
{{- end }}
{{- else if kindIs "slice" $v }}
{{- if and (eq $kk "ipxe") (eq $k "httpScriptExtraKernelArgs") }}
{{ $key }}: {{ join " " (append $v (printf "tink_worker_image=%s:%s" $.Values.deployment.agentImage (coalesce $.Values.deployment.agentImageTag $.Chart.AppVersion))) | quote }}
{{- else if and (eq $kk "tinkController") (hasSuffix "ListRules" $k) }}
{{- /* Reference rules: a list of rule objects rendered as pipe-joined JSON (see docs/technical/REFERENCES.md) */}}
{{- $rules := list }}
{{- range $v }}{{- $rules = append $rules (toJson .) }}{{- end }}
{{ $key }}: {{ join "|" $rules | quote }}
{{- else }}
{{ $key }}: {{ join "," $v | quote }}
{{- end }}
{{- else if kindIs "string" $v }}
{{- if $v }}
{{ $key }}: {{ tpl $v $ | quote }}
{{- end }}
{{- else }}
{{ $key }}: {{ $v | quote }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
{{- range $k, $v := (include "tinkerbell.topLevelEnv" . | fromYaml) }}
{{ $k }}: {{ $v | quote }}
{{- end }}
{{- end -}}

{{/*
tinkerbell.topLevelEnv renders, as YAML, the environment for the top-level
publicIP and trustedProxies values. deployment.envs.globals.publicIpv4 and
deployment.envs.globals.trustedProxies take precedence: they are rendered with
the other globals, so the top-level values are left out when those are set.
*/}}
{{- define "tinkerbell.topLevelEnv" -}}
{{- $env := dict }}
{{- if and .Values.trustedProxies (not .Values.deployment.envs.globals.trustedProxies) }}
{{- $_ := set $env "TINKERBELL_TRUSTED_PROXIES" (join "," .Values.trustedProxies) }}
{{- end }}
{{- if and .Values.publicIP (not .Values.deployment.envs.globals.publicIpv4) }}
{{- $_ := set $env "TINKERBELL_PUBLIC_IP_V4" (toString .Values.publicIP) }}
{{- end }}
{{- toYaml $env }}
{{- end -}}
