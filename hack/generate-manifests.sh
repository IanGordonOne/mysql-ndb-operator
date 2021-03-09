#!/usr/bin/env bash

# Script to generate Ndb CRD and the release artifact

# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_GEN_OPTS="crd:trivialVersions=true"
CRD_GEN_INPUT_PATH="./pkg/apis/..."
CRD_GEN_OUTPUT="helm/crds"
CRD_NAME="helm/crds/mysql.oracle.com_ndbs.yaml"
CONTROLLER_GEN_CMD="go run sigs.k8s.io/controller-tools/cmd/controller-gen"

# Generate Ndb CRD
echo "Generating Ndb CRD..."
${CONTROLLER_GEN_CMD} ${CRD_GEN_OPTS} paths=${CRD_GEN_INPUT_PATH} output:crd:artifacts:config=${CRD_GEN_OUTPUT}
# creationTimestamp in the CRD is always generated as null
# https://github.com/kubernetes-sigs/controller-tools/issues/402
# remove it as a workaround
sed -i.crd.bak "/\ \ creationTimestamp\:\ null/d" ${CRD_GEN_OUTPUT}/* && rm ${CRD_GEN_OUTPUT}/*.crd.bak

# Generate a single ndb-operator yaml file for deploying the CRD and the ndb operator
INSTALL_ARTIFACT="artifacts/install/ndb-operator.yaml"
echo "Generating install artifact..."
# Copy in the Ndb CRD
cp ${CRD_NAME} ${INSTALL_ARTIFACT}
# Generate and append the resources from helm templates
helm template helm >> ${INSTALL_ARTIFACT}
# Prettify the yaml file
go run hack/prettify-yaml.go --yaml=${INSTALL_ARTIFACT}