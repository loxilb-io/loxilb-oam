#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
ENVIRONMENT=""
IMAGE_TAG="latest"
NAMESPACE=""
ENABLE_HTTPS="false"
GENERATE_CERTS="true"

# Function to print colored output
print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_header() {
    echo -e "${BLUE}[DEPLOY]${NC} $1"
}

# Function to show usage
show_usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  -e, --environment    Environment (development|production) [default: development]"
    echo "  -t, --tag           Image tag [default: latest]"
    echo "  -n, --namespace     Kubernetes namespace override"
    echo "  --https             Enable HTTPS deployment [default: false]"
    echo "  --no-certs          Skip certificate generation (use existing certificates)"
    echo "  -h, --help          Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0 -e development"
    echo "  $0 -e production -t v1.0.0 --https"
    echo "  $0 -e development -n my-namespace --https --no-certs"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -e|--environment)
            ENVIRONMENT="$2"
            shift 2
            ;;
        -t|--tag)
            IMAGE_TAG="$2"
            shift 2
            ;;
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        --https)
            ENABLE_HTTPS="true"
            shift
            ;;
        --no-certs)
            GENERATE_CERTS="false"
            shift
            ;;
        -h|--help)
            show_usage
            exit 0
            ;;
        *)
            print_error "Unknown option: $1"
            show_usage
            exit 1
            ;;
    esac
done

# Validate environment
if [[ "$ENVIRONMENT" != "development" && "$ENVIRONMENT" != "production" ]]; then
    print_error "Environment must be either 'development' or 'production'"
    exit 1
fi

# Set namespace if not provided
if [[ -z "$NAMESPACE" ]]; then
    NAMESPACE="oam-loxilb"
fi

print_header "OAM-LoxiLB Kubernetes Deployment"
print_status "Environment: $ENVIRONMENT"
print_status "Image Tag: $IMAGE_TAG"
print_status "Namespace: $NAMESPACE"
print_status "HTTPS Enabled: $ENABLE_HTTPS"
if [ "$ENABLE_HTTPS" = "true" ]; then
    print_status "Generate Certificates: $GENERATE_CERTS"
fi
print_status ""

# Check prerequisites
print_status "Checking prerequisites..."

if ! command -v kubectl >/dev/null 2>&1; then
    print_error "kubectl is not installed. Please install kubectl first."
    exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
    print_error "Docker is not installed. Please install Docker first."
    exit 1
fi

# Check kubectl connection
if ! kubectl cluster-info >/dev/null 2>&1; then
    print_error "Cannot connect to Kubernetes cluster. Please check your kubectl configuration."
    exit 1
fi

print_status "Prerequisites check passed"

# Build and tag the image
print_status "Building OAM-LoxiLB application..."
if [[ "$ENVIRONMENT" == "development" ]]; then
    docker build -t oam-loxilb:$IMAGE_TAG .
else
    docker build -t oam-loxilb:$IMAGE_TAG .
fi

if [ $? -ne 0 ]; then
    print_error "Failed to build application"
    exit 1
fi

print_status "Application built successfully"

# Generate SSL certificates if HTTPS is enabled and requested
if [ "$ENABLE_HTTPS" = "true" ] && [ "$GENERATE_CERTS" = "true" ]; then
    print_status "Generating SSL certificates for HTTPS deployment..."
    
    if [ ! -f "scripts/ssl/generate-dev-certs.sh" ]; then
        print_error "Certificate generation script not found."
        print_error "Please ensure scripts/ssl/generate-dev-certs.sh exists."
        exit 1
    fi
    
    chmod +x scripts/ssl/generate-dev-certs.sh
    if ! ./scripts/ssl/generate-dev-certs.sh; then
        print_warning "Advanced certificate generation failed, trying simple method..."
        chmod +x scripts/ssl/generate-simple-certs.sh
        ./scripts/ssl/generate-simple-certs.sh
    fi
    
    # Update Kubernetes SSL secret
    if [ -f "scripts/ssl/update-k8s-ssl-secret.sh" ]; then
        print_status "Updating Kubernetes SSL secret..."
        chmod +x scripts/ssl/update-k8s-ssl-secret.sh
        NAMESPACE=$NAMESPACE ./scripts/ssl/update-k8s-ssl-secret.sh
    else
        print_warning "SSL secret update script not found. Please update manually."
    fi
elif [ "$ENABLE_HTTPS" = "true" ]; then
    print_status "Using existing SSL certificates for HTTPS deployment..."
    
    # Check if certificates exist
    if [ ! -f "ssl/dev_certs/server.crt" ] || [ ! -f "ssl/dev_certs/server.key" ]; then
        print_error "SSL certificates not found."
        print_error "Please run with --generate-certs or ensure certificates exist."
        exit 1
    fi
fi

# Check if we're using minikube or kind (for local development)
if command -v minikube >/dev/null 2>&1 && minikube status >/dev/null 2>&1; then
    print_status "Loading image into minikube..."
    if [[ "$ENVIRONMENT" == "development" ]]; then
        minikube image load oam-loxilb:$IMAGE_TAG
    else
        minikube image load oam-loxilb:$IMAGE_TAG
    fi
elif command -v kind >/dev/null 2>&1; then
    print_status "Loading image into kind..."
    if [[ "$ENVIRONMENT" == "development" ]]; then
        kind load docker-image oam-loxilb:$IMAGE_TAG
    else
        kind load docker-image oam-loxilb:$IMAGE_TAG
    fi
fi

# Deploy using kustomize
print_status "Deploying to Kubernetes..."

if [[ -n "$NAMESPACE" ]]; then
    # Update kustomization with custom namespace
    if [[ "$ENVIRONMENT" == "development" ]]; then
        sed -i.bak "s/namespace: oam-loxilb-dev/namespace: $NAMESPACE/" k8s/overlays/development/kustomization.yaml
    else
        sed -i.bak "s/namespace: oam-loxilb-prod/namespace: $NAMESPACE/" k8s/overlays/production/kustomization.yaml
    fi
fi

# Choose deployment configuration based on HTTPS setting
if [ "$ENABLE_HTTPS" = "true" ]; then
    print_status "Deploying HTTPS configuration..."
    print_status "Deploying HTTPS configuration using kustomization..."    
    kubectl apply -k k8s/base/    
    
else
    print_status "Deploying HTTP configuration..."
    print_status "Deploying HTTP configuration using kustomization..."    
    kubectl apply -k k8s/base-http/
fi

if [ $? -ne 0 ]; then
    print_error "Failed to deploy to Kubernetes"
    exit 1
fi

# Restore original kustomization file
if [[ -n "$NAMESPACE" ]]; then
    if [[ "$ENVIRONMENT" == "development" ]]; then
        mv k8s/overlays/development/kustomization.yaml.bak k8s/overlays/development/kustomization.yaml
    else
        mv k8s/overlays/production/kustomization.yaml.bak k8s/overlays/production/kustomization.yaml
    fi
fi

# Wait for deployments to be ready
print_status "Waiting for deployments to be ready..."

kubectl wait --for=condition=available --timeout=300s deployment/mysql -n $NAMESPACE
if [ $? -ne 0 ]; then
    print_error "MySQL deployment failed to become available"
    kubectl describe deployment/mysql -n $NAMESPACE
    exit 1
fi

if [ "$ENABLE_HTTPS" = "true" ]; then
    kubectl wait --for=condition=available --timeout=300s deployment/oam-loxilb-https -n $NAMESPACE
    if [ $? -ne 0 ]; then
        print_error "OAM-LoxiLB HTTPS deployment failed to become available"
        kubectl describe deployment/oam-loxilb-https -n $NAMESPACE
        exit 1
    fi
else
    kubectl wait --for=condition=available --timeout=300s deployment/oam-loxilb -n $NAMESPACE
    if [ $? -ne 0 ]; then
        print_error "OAM-LoxiLB deployment failed to become available"
        kubectl describe deployment/oam-loxilb -n $NAMESPACE
        exit 1
    fi
fi

print_status "All deployments are ready"

# Show deployment status
print_status "Deployment Status:"
kubectl get pods -n $NAMESPACE

# Test application health
print_status "Testing application health..."

if [ "$ENABLE_HTTPS" = "true" ]; then
    kubectl port-forward svc/oam-loxilb-https-service 8443:443 -n $NAMESPACE >/dev/null 2>&1 &
    PORT_FORWARD_PID=$!
    sleep 5
    
    max_attempts=10
    attempt=1
    
    while [ $attempt -le $max_attempts ]; do
        if curl -s -k https://localhost:8443/oam/health >/dev/null 2>&1; then
            print_status "HTTPS application is healthy and ready"
            break
        else
            print_warning "Attempt $attempt/$max_attempts: HTTPS application not ready yet, waiting..."
            sleep 10
            attempt=$((attempt + 1))
        fi
    done
else
    kubectl port-forward svc/oam-loxilb-service 8080:8080 -n $NAMESPACE >/dev/null 2>&1 &
    PORT_FORWARD_PID=$!
    sleep 5
    
    max_attempts=10
    attempt=1
    
    while [ $attempt -le $max_attempts ]; do
        if curl -s http://localhost:8080/oam/health >/dev/null 2>&1; then
            print_status "Application is healthy and ready"
            break
        else
            print_warning "Attempt $attempt/$max_attempts: Application not ready yet, waiting..."
            sleep 10
            attempt=$((attempt + 1))
        fi
    done
fi

# Kill port-forward
kill $PORT_FORWARD_PID 2>/dev/null || true

if [ $attempt -gt $max_attempts ]; then
    print_warning "Could not verify application health via port-forward"
    print_warning "This might be normal if ingress is configured"
fi

print_status ""
print_header "Deployment completed successfully"
print_status ""
print_status "Namespace: $NAMESPACE"
print_status "Environment: $ENVIRONMENT"
print_status ""
print_status "Access Commands:"
if [ "$ENABLE_HTTPS" = "true" ]; then
    print_status "  Port Forward: kubectl port-forward svc/oam-loxilb-https-service 8443:443 -n $NAMESPACE"
    print_status "  Then access: https://localhost:8443 (accept self-signed certificate)"
    print_status "  Health check: https://localhost:8443/oam/health"
else
    print_status "  Port Forward: kubectl port-forward svc/oam-loxilb-service 8080:8080 -n $NAMESPACE"
    print_status "  Then access: http://localhost:8080"
    print_status "  Health check: http://localhost:8080/oam/health"
fi
print_status ""
print_status "Useful Commands:"
print_status "  Check pods: kubectl get pods -n $NAMESPACE"
if [ "$ENABLE_HTTPS" = "true" ]; then
    print_status "  View logs: kubectl logs -f deployment/oam-loxilb-https -n $NAMESPACE"
    print_status "  Describe deployment: kubectl describe deployment/oam-loxilb-https -n $NAMESPACE"
    print_status "  Check SSL secret: kubectl describe secret oam-loxilb-ssl-certs -n $NAMESPACE"
else
    print_status "  View logs: kubectl logs -f deployment/oam-loxilb -n $NAMESPACE"
    print_status "  Describe deployment: kubectl describe deployment/oam-loxilb -n $NAMESPACE"
fi
print_status "  Delete deployment: kubectl delete namespace $NAMESPACE"
print_status ""
print_status "Default credentials:"
print_status "  Username: admin"
print_status "  Password: set via OAM_DEFAULT_ADMIN_PASSWORD (hashed in database)"