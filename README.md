# Deploying Keploy with ArgoCD on Kubernetes

This repo shows how to deploy **Keploy's k8s-proxy** and a sample application to a Kubernetes cluster using **ArgoCD** (GitOps), with **Contour** as the ingress controller, for both **staging** and **production** environments.


## How it works

```
┌─────────────────────────────────────────────┐
│  This Git Repo (source of truth)            │
│  ├── environments/staging/    (K8s manifests)│
│  ├── environments/production/ (K8s manifests)│
│  └── argocd/                  (ArgoCD apps)  │
└──────────────────┬──────────────────────────┘
                   │  ArgoCD watches & auto-syncs
                   ▼
┌─────────────────────────────────────────────┐
│  Kubernetes Cluster                          │
│                                              │
│  Namespace: projectcontour                   │
│  └── Contour + Envoy (ingress controller)    │
│       ↓ routes traffic via Ingress/HTTPProxy │
│                                              │
│  Namespace: staging                          │
│  └── sample-order-service (2 replicas)       │
│       ← Ingress: orders.staging.local        │
│                                              │
│  Namespace: keploy                           │
│  └── k8s-proxy (records & replays traffic)   │
│       ← HTTPProxy: <YOUR_INGRESS_HOST>       │
│                                              │
│  Namespace: argocd                           │
│  └── ArgoCD controllers                      │
└─────────────────────────────────────────────┘
```

**ArgoCD** continuously watches this Git repo. When you push a change (e.g. update replicas, change image tag), ArgoCD detects the diff and automatically applies it to the cluster. If someone manually changes the cluster state, ArgoCD self-heals it back to match Git.

**Keploy k8s-proxy** is deployed alongside your app via ArgoCD. It uses a MutatingWebhook to inject an eBPF-based sidecar into your application pods, which captures live traffic without code changes. You can then replay that traffic as automated integration tests from the Keploy UI.

**Contour** is a CNCF ingress controller powered by Envoy proxy. It routes external traffic to both the sample app and the k8s-proxy. The k8s-proxy serves HTTPS on its backend, so we use Contour's **HTTPProxy** CRD (which supports `protocol: tls` for TLS backends) instead of a standard Kubernetes Ingress resource.

## What you need to create for Keploy (ArgoCD)

If you already use ArgoCD to deploy your application, you need:

1. **`keploy-k8s-proxy.yaml`** ArgoCD Application (one per environment) — deploys the k8s-proxy from its OCI Helm chart
2. **`k8s-proxy-httpproxy.yaml`** — a Contour HTTPProxy to route traffic to the k8s-proxy (TLS backend)
3. **An ingress controller** — Contour is used here, but any controller that supports TLS backends works

```
argocd/
├── staging/
│   ├── your-app.yaml              # You already have this
│   └── keploy-k8s-proxy.yaml      # ← CREATE THIS for Keploy
└── production/
    ├── your-app.yaml              # You already have this
    └── keploy-k8s-proxy.yaml      # ← CREATE THIS for Keploy

environments/
├── staging/
│   └── k8s-proxy-httpproxy.yaml   # ← CREATE THIS for Contour routing
└── production/
    └── k8s-proxy-httpproxy.yaml   # ← CREATE THIS for Contour routing
```

**What you also need (one-time, not in Git)**:
- A Kubernetes Secret named `keploy-credentials` in the `keploy` namespace containing your access key
  - The k8s-proxy uses this to authenticate with Keploy's cloud API
  - Get it from Keploy UI: Clusters → Connect New Cluster → enter cluster name and ingress URL → the UI generates the key
  - **Never commit this to Git** — create it manually or via a secrets manager (Sealed Secrets, Vault, External Secrets Operator)

**What you do NOT need to change**: Your existing application code, K8s manifests, and ArgoCD Applications remain untouched. Keploy is completely non-invasive.

## Repository structure

```
.
├── sample-app/                    # Sample Go REST API (Order Service)
│   ├── main.go
│   ├── go.mod
│   └── Dockerfile
│
├── environments/
│   ├── staging/
│   │   ├── namespace.yaml
│   │   ├── deployment.yaml              # 2 replicas, lower resources
│   │   ├── service.yaml
│   │   ├── ingress.yaml                 # Contour Ingress for the sample app
│   │   └── k8s-proxy-httpproxy.yaml     # Contour HTTPProxy for k8s-proxy (TLS backend)
│   └── production/
│       ├── namespace.yaml
│       ├── deployment.yaml              # 3 replicas, higher resources
│       ├── service.yaml
│       ├── ingress.yaml                 # Contour Ingress for the sample app
│       └── k8s-proxy-httpproxy.yaml     # Contour HTTPProxy for k8s-proxy (TLS backend)
│
└── argocd/
    ├── staging/
    │   ├── sample-order-service.yaml    # ArgoCD app → environments/staging/
    │   ├── keploy-k8s-proxy.yaml        # ArgoCD app → Keploy Helm chart ← KEPLOY-SPECIFIC
    │   └── contour.yaml                 # Contour deployment instructions
    └── production/
        ├── sample-order-service.yaml    # ArgoCD app → environments/production/
        ├── keploy-k8s-proxy.yaml        # ArgoCD app → Keploy Helm chart ← KEPLOY-SPECIFIC
        └── contour.yaml                 # Contour deployment instructions
```

### Why HTTPProxy instead of Ingress for k8s-proxy?

The k8s-proxy serves HTTPS (TLS) on its backend port. A standard Kubernetes Ingress resource only supports plain HTTP backends. Contour's [HTTPProxy CRD](https://projectcontour.io/docs/1.30/config/fundamentals/) lets you set `protocol: tls` on a backend service, so Envoy connects to the k8s-proxy over TLS without needing TLS termination at the ingress level.

The sample-order-service uses a standard Ingress (plain HTTP backend), while the k8s-proxy uses an HTTPProxy (TLS backend).

### Staging vs Production differences

| Setting | Staging | Production |
|---------|---------|------------|
| App replicas | 2 | 3 |
| CPU limit | 200m | 500m |
| Memory limit | 128Mi | 256Mi |
| k8s-proxy replicas | 1 | 2 |
| Keploy API server | `api.staging.keploy.io` | `api.keploy.io` |
| Envoy service type | NodePort (30080/30443) | LoadBalancer |
| k8s-proxy routing | HTTPProxy (TLS backend) | HTTPProxy (TLS backend) |

---

## Setup Guide

### Prerequisites

- A Kubernetes cluster (Kind, EKS, GKE, AKS, etc.)
- `kubectl` configured to talk to the cluster
- `helm` v3 installed
- ArgoCD installed on the cluster (see Step 2)
- A Keploy account ([app.keploy.io](https://app.keploy.io) for production, [app.staging.keploy.io](https://app.staging.keploy.io) for staging)

### Step 1: Set up a Kind cluster (local/VM only)

> Skip this if you already have a cluster (EKS, GKE, etc.)

```bash
cat <<EOF > kind-config.yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 30080
        hostPort: 30080
        protocol: TCP
      - containerPort: 30443
        hostPort: 30443
        protocol: TCP
EOF

kind create cluster --config kind-config.yaml
```

The port mappings expose Contour's Envoy proxy ports (HTTP 30080, HTTPS 30443) on your host machine. All ingress traffic flows through these ports.

### Step 2: Install ArgoCD

```bash
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
kubectl -n argocd rollout status deployment/argocd-server
```

Get the admin password:

```bash
kubectl -n argocd get secret argocd-initial-admin-secret \
  -o jsonpath="{.data.password}" | base64 -d; echo
```

Access the ArgoCD UI:

```bash
kubectl -n argocd port-forward svc/argocd-server 8443:443 &
# Open https://localhost:8443 in your browser
# Login with username: admin, password: <from above>
```

### Step 3: Deploy Contour ingress controller

Deploy the official Contour manifests:

```bash
kubectl apply -f https://projectcontour.io/quickstart/contour.yaml
```

**For VM / Kind clusters** — patch Envoy to use NodePort so traffic is accessible on host ports:

```bash
kubectl patch svc envoy -n projectcontour --type='json' -p='[
  {"op": "replace", "path": "/spec/type", "value": "NodePort"},
  {"op": "replace", "path": "/spec/ports/0/nodePort", "value": 30080},
  {"op": "replace", "path": "/spec/ports/1/nodePort", "value": 30443}
]'
```

**For cloud clusters (EKS/GKE/AKS)** — skip the patch. The default LoadBalancer type is correct.

Verify:

```bash
kubectl get pods -n projectcontour
kubectl get svc  -n projectcontour
# Envoy should show NodePort 30080/30443 (VM) or an EXTERNAL-IP (cloud)
```

### Step 4: Build and load the sample app (Kind only)

> Skip if using a container registry. Update `image` and `imagePullPolicy` in the deployment manifests.

```bash
cd sample-app
docker build -t sample-order-service:latest .
kind load docker-image sample-order-service:latest
```

### Step 5: Create the Keploy access key secret

Go to the Keploy UI → **Clusters** → **Connect New Cluster**. Enter a cluster name and ingress URL. You'll get an access key.

Create the secret (**never commit this to Git**):

```bash
kubectl create namespace keploy

kubectl -n keploy create secret generic keploy-credentials \
  --from-literal=access-key="<YOUR_ACCESS_KEY>"
```

### Step 6: Apply the HTTPProxy for k8s-proxy

The k8s-proxy serves HTTPS on its backend, so it needs a Contour HTTPProxy (not a standard Ingress):

```bash
# Edit the fqdn in the file to match your ingress hostname first!
kubectl apply -f environments/staging/k8s-proxy-httpproxy.yaml
```

Verify:

```bash
kubectl get httpproxy -A
# Should show status: valid
```

### Step 7: Deploy with ArgoCD

Deploy the sample app and k8s-proxy:

```bash
kubectl apply -f argocd/staging/keploy-k8s-proxy.yaml
kubectl apply -f argocd/staging/sample-order-service.yaml
```

For production:

```bash
kubectl apply -f environments/production/k8s-proxy-httpproxy.yaml
kubectl apply -f argocd/production/keploy-k8s-proxy.yaml
kubectl apply -f argocd/production/sample-order-service.yaml
```

### Step 8: Verify

```bash
# Check ArgoCD apps
kubectl get applications -n argocd

# Check Contour + Envoy
kubectl get pods -n projectcontour

# Check Ingress and HTTPProxy resources
kubectl get ingress -A
kubectl get httpproxy -A

# Check app pods
kubectl get pods -n staging
kubectl get pods -n keploy

# Test sample app through Contour
curl -H "Host: orders.staging.local" http://localhost:30080/healthz
# → {"status":"healthy","service":"sample-order-service"}

# Test k8s-proxy through Contour (TLS backend)
curl -H "Host: <YOUR_INGRESS_HOST>" http://localhost:30080/healthz
# → {"status":"ok"}
```

In the ArgoCD UI (`https://localhost:8443`), you'll see your applications with their sync status and a visual resource tree.

---

## Customizing for your application

### Template: keploy-k8s-proxy.yaml (ArgoCD Application)

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: keploy-k8s-proxy
  namespace: argocd
  finalizers:
    - resources-finalizer.argocd.argoproj.io
spec:
  project: default

  source:
    chart: k8s-proxy-chart
    repoURL: registry-1.docker.io/keploy
    targetRevision: "3.3.10"                     # Keploy k8s-proxy chart version
    helm:
      values: |
        replicaCount: 1
        environment: "<staging|production>"
        selfHosted: false

        keploy:
          existingSecret: "keploy-credentials"
          existingSecretKey: "access-key"
          clusterName: "<YOUR_CLUSTER_NAME>"      # From Keploy UI
          apiServerUrl: "<KEPLOY_API_URL>"        # https://api.staging.keploy.io or https://api.keploy.io
          ingressUrl: "<YOUR_INGRESS_URL>"        # e.g. https://proxy.example.com:30080

        service:
          type: ClusterIP                         # Routed via Contour HTTPProxy

        mongodb:
          enabled: false
        minio:
          enabled: false

  destination:
    server: https://kubernetes.default.svc
    namespace: keploy

  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

### Template: k8s-proxy-httpproxy.yaml (Contour HTTPProxy)

```yaml
apiVersion: projectcontour.io/v1
kind: HTTPProxy
metadata:
  name: k8s-proxy-ingress
  namespace: keploy
spec:
  virtualhost:
    fqdn: <YOUR_INGRESS_HOST>     # Must match the host in keploy.ingressUrl
  routes:
    - conditions:
        - prefix: /
      services:
        - name: k8s-proxy
          port: 8080
          protocol: tls             # k8s-proxy serves HTTPS on its backend
```

### What you do NOT need to change

- Your existing application code — no SDK, no annotations, no sidecar config
- Your existing K8s manifests — no changes to deployments, services, or pods
- Your existing ArgoCD Applications — Keploy runs alongside, not inside, your app

### Important: never commit secrets

The `keploy-credentials` secret (access key) must be created manually or via a secrets manager (e.g. Sealed Secrets, External Secrets Operator, Vault). It is **not** stored in this repo.

---

## Or use Helm directly (without ArgoCD)

If you don't use ArgoCD, deploy the k8s-proxy with Helm:

```bash
helm upgrade --install k8s-proxy oci://docker.io/keploy/k8s-proxy-chart --version 3.3.10 \
  --namespace keploy \
  --create-namespace \
  --set keploy.accessKey="<YOUR_ACCESS_KEY>" \
  --set keploy.clusterName="<YOUR_CLUSTER_NAME>" \
  --set selfHosted=false \
  --set keploy.ingressUrl="https://<YOUR_INGRESS_HOST>:30080" \
  --set service.type=ClusterIP
```

Then apply the HTTPProxy:

```bash
kubectl apply -f environments/staging/k8s-proxy-httpproxy.yaml
```

---

## Integration Testing with Keploy (UI walkthrough)

Once the k8s-proxy is connected, you can record live traffic and replay it as tests — all from the Keploy UI.

### Recording traffic

1. Go to the Keploy UI → **Clusters** → click your cluster
2. Go to the **Deployments** tab
3. Find your deployment (e.g. `sample-order-service` in `staging`)
4. Click the **Record** button (red circle icon)
5. A 3-step wizard opens:
   - **Step 1 - Pods**: Choose how many pods to record from (slider)
   - **Step 2 - Record config**: Add filters to include/exclude specific traffic (path regex, HTTP methods, headers). Configure concurrent sampling rate (default: 5) and static dedup
   - **Step 3 - Auto replay**: Configure automatic replay during recording (interval, noise fields, env overrides)
6. Click **Start Recording**
7. Send traffic to your application (curl, browser, load test, etc.)
8. The UI shows recorded endpoints in real-time (method, path, status code)
9. Click **Stop** when done

### Replaying traffic (running tests)

1. After recording, click the **Replay** button (play icon) next to your deployment
2. A 2-step wizard opens:
   - **Step 1 - Select tests**: Choose which test sets to replay
   - **Step 2 - Configure**: Set API timeout, delay between tests, noise fields, env overrides
3. Click **Start Replay**
4. The UI shows live progress: passed/failed/noisy counts per test set
5. When complete, review results

### What Keploy does under the hood

1. **Record**: k8s-proxy injects an eBPF agent sidecar into your pods via MutatingWebhook. The agent captures all incoming HTTP requests and outgoing calls (DB queries, external APIs) without code changes
2. **Replay**: k8s-proxy creates an isolated copy of your app, replays the recorded requests, mocks the external dependencies, and compares responses to detect regressions

---

## GitOps workflow

### Deploying a new version

1. Update the image tag in `environments/staging/deployment.yaml`
2. Push to `main`
3. ArgoCD detects the change and rolls out the new version
4. After validating in staging, update `environments/production/deployment.yaml`
5. Push again — ArgoCD deploys to production

### Scaling

1. Change `replicas` in the deployment manifest
2. Push to Git
3. ArgoCD auto-scales

### Rollback

1. `git revert` the bad commit
2. Push — ArgoCD rolls back the cluster state

ArgoCD also **self-heals**: if someone manually deletes a pod or changes a deployment, ArgoCD detects the drift and restores the desired state from Git.
