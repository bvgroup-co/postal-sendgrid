#!/usr/bin/env sh
set -eu

TAG="${1:-${GITHUB_REF_NAME:-}}"
CHART_DIR="${CHART_DIR:-charts/postal-sendgrid}"

if [ -z "$TAG" ]; then
  echo "release tag is required" >&2
  exit 1
fi

case "$TAG" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *)
    echo "tag must match vX.Y.Z: $TAG" >&2
    exit 1
    ;;
esac

VERSION="${TAG#v}"
case "$VERSION" in
  *[!0-9.]*|*.*.*.*|.*|*.|*..*)
    echo "tag must match vX.Y.Z: $TAG" >&2
    exit 1
    ;;
esac

IFS=. read -r MAJOR MINOR PATCH <<EOF_VERSION
$VERSION
EOF_VERSION

if [ -z "$MAJOR" ] || [ -z "$MINOR" ] || [ -z "$PATCH" ]; then
  echo "tag must match vX.Y.Z: $TAG" >&2
  exit 1
fi

for part in "$MAJOR" "$MINOR" "$PATCH"; do
  case "$part" in
    ''|*[!0-9]*)
      echo "tag must match vX.Y.Z: $TAG" >&2
      exit 1
      ;;
  esac
done

CHART_VERSION="$(awk '$1 == "version:" {print $2}' "$CHART_DIR/Chart.yaml")"
APP_VERSION="$(awk '$1 == "appVersion:" {print $2}' "$CHART_DIR/Chart.yaml")"
IMAGE_TAG="$(awk '
  $1 == "image:" {in_image=1; next}
  in_image && $1 == "tag:" {print $2; exit}
  in_image && /^[^[:space:]]/ {in_image=0}
' "$CHART_DIR/values.yaml")"

if [ "$CHART_VERSION" != "$VERSION" ]; then
  echo "Chart.yaml version must be $VERSION, got $CHART_VERSION" >&2
  exit 1
fi

if [ "$APP_VERSION" != "$TAG" ]; then
  echo "Chart.yaml appVersion must be $TAG, got $APP_VERSION" >&2
  exit 1
fi

if [ "$IMAGE_TAG" != "$TAG" ]; then
  echo "values.yaml image.tag must be $TAG, got $IMAGE_TAG" >&2
  exit 1
fi
