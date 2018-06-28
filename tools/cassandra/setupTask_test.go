// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cassandra

import (
	"fmt"
	"github.com/gocql/gocql"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/uber-common/bark"
	"math/rand"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"strconv"
)

type (
	SetupSchemaTestSuite struct {
		*require.Assertions // override suite.Suite.Assertions with require.Assertions; this means that s.NotNil(nil) will stop the test, not merely log an error
		suite.Suite
		rand     *rand.Rand
		keyspace string
		session  *gocql.Session
		client   CQLClient
		log      bark.Logger
	}
)

func TestSetupSchemaTestSuite(t *testing.T) {
	suite.Run(t, new(SetupSchemaTestSuite))
}

func (s *SetupSchemaTestSuite) SetupTest() {
	s.Assertions = require.New(s.T()) // Have to define our overridden assertions in the test setup. If we did it earlier, s.T() will return nil
}

func (s *SetupSchemaTestSuite) SetupSuite() {
	s.log = bark.NewLoggerFromLogrus(log.New())
	s.rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	s.keyspace = fmt.Sprintf("setup_schema_test_%v", s.rand.Int63())

	client, err := newCQLClient("127.0.0.1", defaultCassandraPort, "", "", "system", defaultTimeout)
	if err != nil {
		s.log.Fatal("Error creating CQLClient")
	}

	err = client.CreateKeyspace(s.keyspace, 1)
	if err != nil {
		log.Fatalf("error creating keyspace, err=%v", err)
	}

	s.client = client
}

func (s *SetupSchemaTestSuite) TearDownSuite() {
	s.client.DropKeyspace(s.keyspace)
	s.client.Close()
}

func (s *SetupSchemaTestSuite) TestCreateKeyspace() {
	RunTool([]string{"./tool", "create", "-k", "foobar123", "--rf", "1"})
	err := s.client.DropKeyspace("foobar123")
	s.Nil(err)
}

func (s *SetupSchemaTestSuite) TestSetupSchema() {

	client, err := newCQLClient("127.0.0.1", defaultCassandraPort, "", "", s.keyspace, defaultTimeout)
	s.Nil(err)

	// test command fails without required arguments
	RunTool([]string{"./tool", "-k", s.keyspace, "-q", "setup-schema"})
	tables, err := client.ListTables()
	s.Nil(err)
	s.Equal(0, len(tables))

	tmpDir, err := ioutil.TempDir("", "setupSchemaTestDir")
	s.Nil(err)
	defer os.Remove(tmpDir)

	cqlFile, err := ioutil.TempFile(tmpDir, "setupSchema.cliOptionsTest")
	s.Nil(err)
	defer os.Remove(cqlFile.Name())

	cqlFile.WriteString(createTestCQLFileContent())

	// make sure command doesn't succeed without version or disable-version
	RunTool([]string{"./tool", "-k", s.keyspace, "-q", "setup-schema", "-f", cqlFile.Name()})
	tables, err = client.ListTables()
	s.Nil(err)
	s.Equal(0, len(tables))

	for i := 0; i < 4; i++ {

		ver := strconv.Itoa(int(s.rand.Int31()))
		versioningEnabled := (i%2 == 0)

		// test overwrite with versioning works
		if versioningEnabled {
			RunTool([]string{"./tool", "-k", s.keyspace, "-q", "setup-schema", "-f", cqlFile.Name(), "-version", ver, "-o"})
		} else {
			RunTool([]string{"./tool", "-k", s.keyspace, "-q", "setup-schema", "-f", cqlFile.Name(), "-d", "-o"})
		}

		expectedTables := getExpectedTables(versioningEnabled)
		tables, err = client.ListTables()
		s.Nil(err)
		s.Equal(len(expectedTables), len(tables))

		for _, t := range tables {
			_, ok := expectedTables[t]
			s.True(ok)
			delete(expectedTables, t)
		}
		s.Equal(0, len(expectedTables))

		gotVer, err := client.ReadSchemaVersion()
		if versioningEnabled {
			s.Nil(err)
			s.Equal(ver, gotVer)
		} else {
			s.NotNil(err)
		}
	}
}

func getExpectedTables(versioningEnabled bool) map[string]struct{} {
	expectedTables := make(map[string]struct{})
	expectedTables["tasks"] = struct{}{}
	expectedTables["events"] = struct{}{}
	if versioningEnabled {
		expectedTables["schema_version"] = struct{}{}
		expectedTables["schema_update_history"] = struct{}{}
	}
	return expectedTables
}
