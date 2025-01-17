// Copyright (c) 2021, Oracle and/or its affiliates.
//
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

package e2e

import (
	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"

	"github.com/mysql/ndb-operator/pkg/apis/ndbcontroller/v1alpha1"
	"github.com/mysql/ndb-operator/pkg/helpers/testutils"

	"github.com/mysql/ndb-operator/e2e-tests/utils/mysql"
	"github.com/mysql/ndb-operator/e2e-tests/utils/ndbtest"
	secretutils "github.com/mysql/ndb-operator/e2e-tests/utils/secret"
)

var _ = ndbtest.DescribeFeature("MySQL Custom cnf", func() {
	var ns string
	var c clientset.Interface
	var ndbName, mysqlRootSecretName string
	var testNdb *v1alpha1.NdbCluster

	ginkgo.BeforeEach(func() {
		ginkgo.By("extracting values from framework")
		f := ndbtest.GetFramework()
		ns = f.Namespace.Name
		c = f.ClientSet

		ginkgo.By("Deploying operator in namespace'" + ns + "'")
		ndbtest.DeployNdbOperator(c, ns)
	})

	ginkgo.AfterEach(func() {
		ginkgo.By("Deleting ndb operator and other resources")
		ndbtest.UndeployNdbOperator(c, ns)
	})

	ginkgo.When("a custom cnf property is specified for MySQL Server", func() {

		ginkgo.BeforeEach(func() {
			ndbName = "ndb-custom-cnf-test"
			mysqlRootSecretName = ndbName + "-root-secret"
			// create the secret first
			secretutils.CreateSecretForMySQLRootAccount(c, mysqlRootSecretName, ns)
			// create the Ndb resource
			testNdb = testutils.NewTestNdb(ns, ndbName, 2)
			testNdb.Spec.Mysqld.RootPasswordSecretName = mysqlRootSecretName
			testNdb.Spec.Mysqld.MyCnf = "[mysqld]\nmax-user-connections=42\nlog-bin=ON"
			ndbtest.KubectlApplyNdbObj(c, testNdb)
		})

		ginkgo.AfterEach(func() {
			// cleanup
			ndbtest.KubectlDeleteNdbObj(c, testNdb)
			// drop the secret
			secretutils.DeleteSecret(c, mysqlRootSecretName, ns)
		})

		ginkgo.It("should start the server with those values as the defaults", func() {
			db := mysql.Connect(c, testNdb, "performance_schema")

			ginkgo.By("verifying that max_user_connections is properly set in server", func() {
				row := db.QueryRow(
					"select variable_value from global_variables where variable_name = 'max_user_connections';")
				var value int
				framework.ExpectNoError(row.Scan(&value),
					"querying for max_user_connections returned an error")
				gomega.Expect(value).To(gomega.Equal(42),
					"max_user_connections had an unexpected value")
			})

			ginkgo.By("verifying that the defaults doesn't override the value set by the operator", func() {
				row := db.QueryRow(
					"select variable_value from global_variables where variable_name = 'log_bin';")
				var value string
				framework.ExpectNoError(row.Scan(&value),
					"querying for log_bin returned an error")
				gomega.Expect(value).To(gomega.Or(gomega.Equal("OFF")),
					"log_bin has an unexpected value")
			})
		})
	})
})
