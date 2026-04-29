# openotters-app Helm Chart

## Installation

```bash
helm upgrade --install openotters-app ./helm/openotters-app \
  --namespace openotters \
  --create-namespace
```

## Configuration

| Parameter           | Description                | Default                              |
| ------------------- | -------------------------- | ------------------------------------ |
| `image.repository`  | Image repository           | `ghcr.io/openotters/openotters-app`  |
| `image.tag`         | Image tag                  | `appVersion`                         |
| `ingress.enabled`   | Enable ingress             | `false`                              |
| `api.name`          | Backend API service name   | `openotters-api`                     |
| `api.namespace`     | Backend API namespace      | `openotters`                         |
| `api.port`          | Backend API port           | `8080`                               |

See [values.yaml](./values.yaml) for all configuration options.
