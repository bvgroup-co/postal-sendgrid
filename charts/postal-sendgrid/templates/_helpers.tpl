{{- define "postal-sendgrid.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "postal-sendgrid.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "postal-sendgrid.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "postal-sendgrid.labels" -}}
helm.sh/chart: {{ include "postal-sendgrid.chart" . }}
{{ include "postal-sendgrid.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "postal-sendgrid.selectorLabels" -}}
app.kubernetes.io/name: {{ include "postal-sendgrid.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "postal-sendgrid.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "postal-sendgrid.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "postal-sendgrid.secretName" -}}
{{- if .Values.existingSecret.name -}}
{{- .Values.existingSecret.name -}}
{{- else -}}
{{- default (include "postal-sendgrid.fullname" .) .Values.secret.name -}}
{{- end -}}
{{- end -}}

{{- define "postal-sendgrid.webhookSigningSecretName" -}}
{{- if .Values.webhookSigning.generatedSecretName -}}
{{- .Values.webhookSigning.generatedSecretName -}}
{{- else -}}
{{- printf "%s-webhook-signing" (include "postal-sendgrid.fullname" .) -}}
{{- end -}}
{{- end -}}
