# Keploy ArgoCD Demo

Demonstrates deploying Keploy's k8s-proxy and a sample application to a Kubernetes cluster using ArgoCD.

## What's inside

```
.
├── sample-app/          # A simple Go REST API (Order Service)
│   ├── main.go
│   ├── Dockerfile
│   └── k8s/             # Raw K8s manifests
│       ├── namespace.yaml
│       ├── deployment.yaml
│       └── service.yaml
└── argocd/              # ArgoCD Application definitions
    ├── keploy-k8s-proxy.yaml       # Deploys k8s-proxy from OCI Helm chart
    └── sample-order-service.yaml   # Deploys the sample app from this repo
```

## Quick start

### 1. Set up a Kind cluster

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

### 2. Build and load the sample app

```bash
cd sample-app
docker build -t sample-order-service:latest .
kind load docker-image sample-order-service:latest
```

### 3. Install ArgoCD

```bash
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
kubectl -n argocd rollout status deployment/argocd-server
```

### 4. Get ArgoCD password and login

```bash
# Password
kubectl -n argocd get secret argocd-initial-admin-secret \
  -o jsonpath="{.data.password}" | base64 -d; echo

# CLI login
kubectl -n argocd port-forward svc/argocd-server 8443:443 &
argocd login localhost:8443 --username admin --password <PASSWORD> --insecure
```

### 5. Create the Keploy secret and deploy

```bash
kubectl create namespace keploy
kubectl -n keploy create secret generic keploy-credentials \
  --from-literal=access-key="<YOUR_ACCESS_KEY>"

kubectl apply -f argocd/keploy-k8s-proxy.yaml
kubectl apply -f argocd/sample-order-service.yaml
```

### 6. Verify

```bash
argocd app list
argocd app get sample-order-service
argocd app get keploy-k8s-proxy
kubectl -n staging get pods
```
