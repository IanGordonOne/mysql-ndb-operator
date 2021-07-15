// Copyright (c) 2020, 2021, Oracle and/or its affiliates.
//
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

// Tool to run end to end tests using Kubetest2 and Ginkgo

//+build ignore

package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Command line options
var options struct {
	useKind    bool
	kubeconfig string
	inCluster bool
}

// Constants for the test runner and providers
const (
	// K8s image used by KinD to bring up cluster
	// The k8s 1.20.2 image built for KinD 0.10.0 is used
	// https://github.com/kubernetes-sigs/kind/releases/tag/v0.10.0
	kindK8sImage = "kindest/node:v1.20.2@sha256:8f7ea6e7642c0da54f04a7ee10431549c0257315b3a634f6ef2fecaaedb19bab"
)

var (
	kindCmd = []string{"go", "run", "sigs.k8s.io/kind"}
)

// validateKubeConfig validates the config passed to --kubeconfig
// and verifies that the K8s cluster is running
func validateKubeConfig(kubeconfig string) bool {
	// Read the config from kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Printf(" ❌ Error loading kubeconfig from '%s': %s", kubeconfig, err)
		return false
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Printf(" ❌ Error creating clientset from kubeconfig at '%s': %s", kubeconfig, err)
		return false
	}

	// Retrieve version and verify
	var k8sVersion *version.Info
	if k8sVersion, err = clientset.ServerVersion(); err != nil {
		log.Printf(" ❌ Error finding out the version of the K8s cluster : %s", err)
		return false
	}

	log.Printf(" 👍 Successfully validated kubeconfig. Kubernetes Server version : %s", k8sVersion.String())
	return true
}

// provider is an interface for the k8s cluster providers
type provider interface {
	// setupK8sCluster sets up the provider specific cluster.
	// It returns true if it succeeded in its attempt.
	setupK8sCluster(t *testRunner) bool
	getKubeConfig() string
	teardownK8sCluster(t *testRunner)
}

// local implements a provider to connect to an existing K8s cluster
type local struct {
	// kubeconfig is the kubeconfig of the cluster
	kubeconfig string
}

// newLocalProvider returns a new local provider
func newLocalProvider() *local {
	log.Println("🔧 Configuring tests to run on an existing cluster")
	return &local{}
}

// setupK8sCluster just validates the kubeconfig passed
// as the cluster is expected to be running already.
// It returns true if it succeeded in its attempt.
func (l *local) setupK8sCluster(*testRunner) bool {
	// Validate the kubeconfig
	if len(options.kubeconfig) > 0 {
		if validateKubeConfig(options.kubeconfig) {
			l.kubeconfig = options.kubeconfig
			return true
		}
	}

	log.Println("⚠️  Please pass a valid kubeconfig")
	return false
}

// getKubeConfig returns the Kubeconfig to connect to the cluster
func (l *local) getKubeConfig() string {
	return l.kubeconfig
}

// teardownK8sCluster is a no-op for local provider
// TODO: Maybe verify all the test resources are cleaned up here?
func (l *local) teardownK8sCluster(*testRunner) {}

// kind implements a provider to control k8s clusters in KinD
type kind struct {
	// kubeconfig is the kubeconfig of the cluster
	kubeconfig string
	// cluster name
	clusterName string
	// kubernetes clientset
	clientset *kubernetes.Clientset
}

// newKindProvider returns a new kind provider
func newKindProvider() *kind {
	log.Println("🔧 Configuring tests to run on a KinD cluster")
	return &kind{}
}

// setupK8sCluster starts a K8s cluster using KinD
func (k *kind) setupK8sCluster(t *testRunner) bool {
	// Verify that docker is running
	if !t.execCommand([]string{"docker", "info"}, "docker info", true, false) {
		// Docker not running. Exit here as there is nothing to cleanup.
		log.Fatal("⚠️  Please ensure that docker daemon is running and accessible.")
	}
	log.Println("🐳 Docker daemon detected and accessible!")

	if !k.createKindCluster(t) {
		return false
	}

	// Load the operator docker image into cluster nodes
	if !k.loadImageToKindCluster("mysql/ndb-operator:latest", t) {
		return false
	}

	if options.inCluster {
		// Load e2e-tests docker image into cluster nodes
		if !k.loadImageToKindCluster("e2e-tests:latest", t) {
			return false
		}
	}
	return true
}

// createKindCluster creates a kind cluster 'ndb-e2e-test'
// It returns true on success.
func (k *kind) createKindCluster(t *testRunner) bool {
	// custom kubeconfig
	k.kubeconfig = filepath.Join(t.testDir, "_artifacts", ".kubeconfig")
	// kind cluster name
	k.clusterName = "ndb-e2e-test"
	// Build KinD command and args
	kindCreateCluster := append(kindCmd,
		// create cluster
		"create", "cluster",
		// cluster name
		"--name="+k.clusterName,
		// kubeconfig
		"--kubeconfig="+k.kubeconfig,
		// k8s image to be used
		"--image="+kindK8sImage,
		// cluster configuration
		"--config="+filepath.Join(t.testDir, "_config", "kind-3-node-cluster.yaml"),
	)

	// Run the command to create a cluster
	if !t.execCommand(kindCreateCluster, "kind create cluster", false, true) {
		log.Println("❌ Failed to create cluster using KinD")
		return false
	}
	log.Println("✅ Successfully started a KinD cluster")
	return true
}

// loadImageToKindCluster loads docker image to kind cluster
// It returns true on success.
func (k *kind) loadImageToKindCluster(image string, t *testRunner) bool {
	kindLoadImage := append(kindCmd,
		// load docker-image
		"load", "docker-image",
		// image name
		image,
		// cluster name
		"--name="+k.clusterName,
	)
	// Run the command to load docker image
	if !t.execCommand(kindLoadImage, "kind load docker-image", false, true) {
		log.Printf("❌ Failed to load '%s' image into KinD cluster", image)
		return false
	}
	log.Printf("✅ Successfully loaded '%s' image into the KinD cluster", image)
	return true
}

// getKubeConfig returns the Kubeconfig to connect to the cluster
func (k *kind) getKubeConfig() string {
	return k.kubeconfig
}

// getPodPhase returns a pod's phase in a given namespace
func (k *kind) getPodPhase(namespace string, podName string) (v1.PodPhase, error) {
	pod, err := k.clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return pod.Status.Phase, nil
}

// hasPodSucceeded checks if a given pod in a given namespace,
// has succeeded its execution.
// It returns true if pod has reached 'Succeeded' phase
func (k *kind) hasPodSucceeded(namespace string, podName string) bool {
	podPhase, err := k.getPodPhase(namespace, podName)
	if err != nil {
		log.Printf("❌ Error getting '%s' pod's phase: %s", podName, err)
		return false
	}

	if podPhase == v1.PodSucceeded  {
		return true
	}
	return false
}

// isPodRunning checks if a given pod in a given namespace
// returns true, if pod is running
func (k *kind) isPodRunning(namespace string, podName string) (bool, error) {
		podPhase, err := k.getPodPhase(namespace, podName)
		if err != nil {
			return false, err
		}

		if podPhase == v1.PodRunning {
			return true, nil
		}
		// return false, nil by default,
		// indicating pod is in 'Pending' phase
		return false, nil
}

// teardownK8sCluster deletes the KinD cluster
func (k *kind) teardownK8sCluster(t *testRunner) {
	// Build KinD command and args
	kindCmdAndArgs := append(kindCmd,
		// create cluster
		"delete", "cluster",
		// cluster name
		"--name="+k.clusterName,
		// kubeconfig
		"--kubeconfig="+k.kubeconfig,
	)

	// Run the command
	t.execCommand(kindCmdAndArgs, "kind delete cluster", false, true)
}

//go run sigs.k8s.io/kind delete cluster --kubeconfig=/tmp/kubeconfig --name=ndb-e2e-test

// testRunner is the struct used to run the e2e test
type testRunner struct {
	// testDir is the absolute path of e2e test directory
	testDir string
	// p is the provider used to execute the test
	p provider
	// sigMutex is the mutex used to protect process
	// and ignoreSignals access across goroutines
	sigMutex sync.Mutex
	// process started by the execCommand
	// used to send signals when it is running
	// Access should be protected by sigMutex
	process *os.Process
	// passSignals enables passing signals to the
	// process started by the testRunner
	// Access should be protected by sigMutex
	passSignals bool
	// runDone is the channel used to signal that
	// the run method has completed. Used by
	// signalHandler to stop listening for signals
	runDone chan bool
}

// init sets up the the testRunner
func (t *testRunner) init() {
	// Update log to print only line numbers
	log.SetFlags(log.Lshortfile)

	// Deduce test root directory
	_, currentFilePath, _, _ := runtime.Caller(0)
	t.testDir = filepath.Dir(currentFilePath)
}

func (t *testRunner) startSignalHandler() {
	// Create the runDone channel
	t.runDone = make(chan bool)
	// Start a go routine to handle signals
	go func() {
		// Create a channel to receive any signal
		sigs := make(chan os.Signal, 2)
		signal.Notify(sigs)

		// Handle all the signals as follows
		// - If a process is running and ignoreSignals
		//   is enabled, send the signal to the process.
		// - If a process is running and ignoreSignals
		//   is disabled, ignore the signal
		// - If no process is running, handle it appropriately
		// - Return when the main return signals done
		for {
			select {
			case sig := <-sigs:
				t.sigMutex.Lock()
				if t.process != nil {
					if t.passSignals {
						// Pass the signal to the process
						_ = t.process.Signal(sig)
					} // else it is ignored
				} else {
					// no process running - handle it
					if sig == syscall.SIGINT ||
						sig == syscall.SIGQUIT ||
						sig == syscall.SIGTSTP {
						// Test is being aborted
						// teardown the cluster and exit
						t.p.teardownK8sCluster(t)
						log.Fatalf("⚠️  Test was aborted!")
					}
				}
				t.sigMutex.Unlock()
			case <-t.runDone:
				// run method has completed - stop signal handler
				return
			}
		}
	}()
}

// stopSignalHandler stops the signal handler
func (t *testRunner) stopSignalHandler() {
	t.runDone <- true
}

// execCommand executes the command along with its arguments
// passed through commandAndArgs slice. commandName is a log
// friendly name of the command to be used in the logs. The
// command output can be suppressed by enabling the quiet
// parameter. passSignals should be set to true if the
// signals received by the testRunner needs be passed to the
// process started by this function.
// It returns true id command got executed successfully
// and false otherwise.
func (t *testRunner) execCommand(
	commandAndArgs []string, commandName string, quiet bool, passSignals bool) bool {
	// Create cmd struct
	cmd := exec.Command(commandAndArgs[0], commandAndArgs[1:]...)

	// Map the stout and stderr if not quiet
	if !quiet {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	// Protect cmd.Start() by sigMutex to avoid signal
	// handler wrongly processing the signals itself
	// after the process has been started
	t.sigMutex.Lock()

	// Start the command
	if err := cmd.Start(); err != nil {
		log.Printf("❌ Starting '%s' failed : %s", commandName, err)
		return false
	}

	// Setup variables to be used the signal handler
	// before unlocking the sigMutex
	t.process = cmd.Process
	t.passSignals = passSignals
	t.sigMutex.Unlock()
	defer func() {
		// Reset the signal handler variables before returning
		t.sigMutex.Lock()
		t.process = nil
		t.sigMutex.Unlock()
	}()

	// Wait for the process to complete
	if err := cmd.Wait(); err != nil {
		log.Printf("❌ Running '%s' failed : %s", commandName, err)
		return false
	}

	return true
}

// runGinkgoTests runs all the tests using ginkgo
func (t *testRunner) runGinkgoTests() {
	// The ginkgo test command
	ginkgoTest := []string{
		"go", "run", "github.com/onsi/ginkgo/ginkgo",
		"-r", // recursively run all suites in the given directory
		"-keepGoing", // keep running all test suites even if one fails
	}

	// Append the ginkgo directory to run the test on
	ginkgoTest = append(ginkgoTest, filepath.Join(t.testDir, "suites"))

	// Append arguments to pass to the testcases
	ginkgoTest = append(ginkgoTest, "--", "--kubeconfig="+t.p.getKubeConfig())

	// Execute it
	log.Println("🔨 Running tests using ginkgo : " + strings.Join(ginkgoTest, " "))
	if t.execCommand(ginkgoTest, "ginkgo", false, true) {
		log.Println("😊 All tests ran successfully!")
	}
}

// run executes the complete e2e test
func (t *testRunner) run() {
	// Choose a provider
	var p provider
	if options.useKind {
		p = newKindProvider()
	} else {
		p = newLocalProvider()
	}
	// store it in testRunner
	t.p = p

	// Start signal handler
	t.startSignalHandler()

	// Setup the K8s cluster
	if !p.setupK8sCluster(t) {
		// Failed to setup cluster.
		// Cleanup resources and exit.
		p.teardownK8sCluster(t)
		t.stopSignalHandler()
		os.Exit(1)
	}

	// Run the tests
	t.runGinkgoTests()

	// Cleanup resources and return
	p.teardownK8sCluster(t)
	t.stopSignalHandler()
}

func init() {

	flag.BoolVar(&options.useKind, "use-kind", false,
		"Use KinD to run the e2e tests.\nBy default, this is disabled and the tests will be run in an existing K8s cluster.")

	// use kubeconfig at $HOME/.kube/config as the default
	defaultKubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	flag.StringVar(&options.kubeconfig, "kubeconfig", defaultKubeconfig,
		"Kubeconfig of the existing K8s cluster to run tests on.\nThis will not be used if '--use-kind' is enabled.")

	flag.BoolVar(&options.inCluster, "in-cluster", false,
		"Run tests as K8s pod inside cluster.")
}

func main() {
	flag.Parse()
	t := testRunner{}
	t.init()
	t.run()
}
