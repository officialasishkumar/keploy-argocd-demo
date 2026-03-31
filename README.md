# Deploying Keploy with ArgoCD on Kubernetes

This repo shows how to deploy **Keploy's k8s-proxy** and a sample application to a Kubernetes cluster using **ArgoCD** (GitOps), for both **staging** and **production** environments.


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
│  Namespace: staging                          │
│  └── sample-order-service (2 replicas)       │
│                                              │
│  Namespace: production                       │
│  └── sample-order-service (3 replicas)       │
│                                              │
│  Namespace: keploy                           │
│  └── k8s-proxy (records & replays traffic)   │
│                                              │
│  Namespace: argocd                           │
│  └── ArgoCD controllers                      │
└─────────────────────────────────────────────┘
```

**ArgoCD** continuously watches this Git repo. When you push a change (e.g. update replicas, change image tag), ArgoCD detects the diff and automatically applies it to the cluster. If someone manually changes the cluster state, ArgoCD self-heals it back to match Git.

**Keploy k8s-proxy** is deployed alongside your app via ArgoCD. It uses a MutatingWebhook to inject an eBPF-based sidecar into your application pods, which captures live traffic without code changes. You can then replay that traffic as automated integration tests from the Keploy UI.

## Repository structure

```
.
├── sample-app/                    # Sample Go REST API (Order Service)
│   ├── main.go
│   ├── go.mod
│   └── Dockerfile
│
├── environments/
│   ├── staging/                   # K8s manifests for staging
│   │   ├── namespace.yaml
│   │   ├── deployment.yaml        # 2 replicas, lower resources
│   │   └── service.yaml
│   └── production/                # K8s manifests for production
│       ├── namespace.yaml
│       ├── deployment.yaml        # 3 replicas, higher resources
│       └── service.yaml
│
└── argocd/
    ├── staging/
    │   ├── sample-order-service.yaml   # ArgoCD app → environments/staging/
    │   └── keploy-k8s-proxy.yaml       # ArgoCD app → Keploy Helm chart (staging)
    └── production/
        ├── sample-order-service.yaml   # ArgoCD app → environments/production/
        └── keploy-k8s-proxy.yaml       # ArgoCD app → Keploy Helm chart (production)
```

### Staging vs Production differences

| Setting | Staging | Production |
|---------|---------|------------|
| App replicas | 2 | 3 |
| CPU limit | 200m | 500m |
| Memory limit | 128Mi | 256Mi |
| k8s-proxy replicas | 1 | 2 |
| Keploy API server | `api.staging.keploy.io` | `api.keploy.io` |
| Service type (proxy) | NodePort (30080) | ClusterIP (use Ingress/LB) |

---

## Setup Guide

### Prerequisites

- A Kubernetes cluster (Kind, EKS, GKE, AKS, etc.)
- `kubectl` configured to talk to the cluster
- `helm` v3 installed
- ArgoCD installed on the cluster (see Step 2)
- A Keploy account (https://app.keploy.io for production, https://app.staging.keploy.io for staging)

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
EOF

kind create cluster --config kind-config.yaml
```

The port mapping exposes the k8s-proxy NodePort (30080) on your host machine, so the Keploy UI can reach it.

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

### Step 3: Build and load the sample app (Kind only)

> Skip if using a container registry. In that case, update the `image` and `imagePullPolicy` in the deployment manifests.

```bash
cd sample-app
docker build -t sample-order-service:latest .
kind load docker-image sample-order-service:latest
```

### Step 4: Create the Keploy access key secret

Go to the Keploy UI → **Clusters** → **Connect New Cluster**. Enter a cluster name and ingress URL. You'll get an access key.

Create the secret in your cluster (**never commit this to Git**):

```bash
kubectl create namespace keploy

kubectl -n keploy create secret generic keploy-credentials \
  --from-literal=access-key="<YOUR_ACCESS_KEY>"
```

### Step 5: Deploy with ArgoCD

**For staging:**

```bash
kubectl apply -f argocd/staging/keploy-k8s-proxy.yaml
kubectl apply -f argocd/staging/sample-order-service.yaml
```

**For production:**

```bash
kubectl apply -f argocd/production/keploy-k8s-proxy.yaml
kubectl apply -f argocd/production/sample-order-service.yaml
```

**For both:**

```bash
kubectl apply -f argocd/staging/
kubectl apply -f argocd/production/
```

### Step 6: Verify

```bash
# Check ArgoCD apps
kubectl get applications -n argocd

# Check pods
kubectl get pods -n staging
kubectl get pods -n production
kubectl get pods -n keploy
```

In the ArgoCD UI (`https://localhost:8443`), you'll see your applications with their sync status and a visual resource tree.

---

## Making the Keploy UI reachable (VM / Local setup)

The Keploy UI needs to reach the k8s-proxy running in your cluster. There are two approaches:

### Option A: /etc/hosts + NodePort (simple, for dev/VM)

This is the approach used for staging on a VM.

1. The k8s-proxy is exposed as a NodePort on port 30080
2. On the machine where you open the browser, add an `/etc/hosts` entry:

```bash
# Replace with your VM's IP
sudo sh -c 'echo "192.168.116.129  your-domain.example.io" >> /etc/hosts'
```

3. When creating the cluster in the Keploy UI, use `https://your-domain.example.io:30080` as the ingress URL
4. Visit `https://your-domain.example.io:30080` once in Chrome and accept the self-signed certificate (Advanced → Proceed)
5. You may need to disable Secure DNS in Chrome: `chrome://settings/security` → turn off "Use secure DNS"

### Option B: Ingress / LoadBalancer (production)

For production clusters with a real domain:

1. Set the k8s-proxy service type to `ClusterIP` or `LoadBalancer`
2. Configure an Ingress (nginx, ALB, etc.) that terminates TLS and routes to the k8s-proxy service
3. Use the real domain as the `ingressUrl` in the Helm values

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
   - **Step 2 - Record config**: Add filters to include/exclude specific traffic (path regex, HTTP methods, headers). Configure sampling rate and static dedup
   - **Step 3 - Auto replay**: Configure automatic replay during recording (interval, noise fields, env overrides)
6. Click **Start Recording**
7. Send traffic to your application (curl, browser, load test, etc.)
8. The UI shows recorded endpoints in real-time (method, path, status code)
9. Click **Stop** when done

### Replaying traffic (running tests)

1. After recording, click the **Replay** button (play icon) next to your deployment
2. A 2-step wizard opens:
   - **Step 1 - Select tests**: Choose which test sets to replay. You can select all or pick individual test cases
   - **Step 2 - Configure**: Set API timeout, delay between tests, noise fields (body/header fields to ignore during comparison), env overrides
3. Click **Start Replay**
4. The UI shows live progress: passed/failed/noisy counts per test set
5. When complete, review results

### What Keploy does under the hood

1. **Record**: k8s-proxy injects an eBPF agent sidecar into your pods via MutatingWebhook. The agent captures all incoming HTTP requests and outgoing calls (DB queries, external APIs) without code changes
2. **Replay**: k8s-proxy creates an isolated copy of your app, replays the recorded requests, mocks the external dependencies, and compares responses to detect regressions

---

## GitOps workflow

The key benefit of ArgoCD is that **Git is the single source of truth**. Here's how changes flow:

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

---

## Customizing for your application

To use this with your own application:

1. **Replace the sample app** — put your K8s manifests in `environments/staging/` and `environments/production/`
2. **Update the ArgoCD Application source** — change the `repoURL` and `path` in `argocd/*/sample-order-service.yaml` to point to your repo and manifest path
3. **Update the ingress URL** — set `keploy.ingressUrl` in `argocd/*/keploy-k8s-proxy.yaml` to where your k8s-proxy will be reachable
4. **Create the Keploy secret** — each environment needs its own `keploy-credentials` secret with the access key from the Keploy UI

### Important: never commit secrets

The `keploy-credentials` secret (access key) must be created manually or via a secrets manager (e.g. Sealed Secrets, External Secrets Operator, Vault). It is **not** stored in this repo.
