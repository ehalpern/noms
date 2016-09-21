// Copyright 2016 Attic Labs, Inc. All rights reserved.
// Licensed under the Apache License, version 2.0:
// http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"io"
	"path"
	"testing"

	"github.com/attic-labs/noms/go/perf/suite"
	"github.com/attic-labs/noms/samples/go/csv"
)

// CSV perf suites require the testdata directory to be checked out at $GOPATH/src/github.com/attic-labs/testdata (i.e. ../testdata relative to the noms directory).

type perfSuite struct {
	csv.CsvPerfSuite
	csvImportExe string
}

func (s *perfSuite) SetupSuite() {
	s.CsvPerfSuite.SetupSuite()
}

func (s *perfSuite) TearDownSuite() {
	s.CsvPerfSuite.TearDownSuite()
}

func (s *perfSuite) Test01ImportSfCrimeBlobFromTestdata() {
	ds := "sf-crime"
	s.ImportDataToBlob(ds, "2016-07-28.*", ds+"/raw")
}

func (s *perfSuite) Test02ImportSfCrimeCSVFromBlob() {
	ds := "sf-crime"
	s.ImportBlob(ds+"/raw", s.DatabaseSpec, ds)
}

func (s *perfSuite) Test03ImportSfRegisteredBusinessesFromBlobAsMap() {
	ds := "sf-registered-businesses"
	s.ImportDataToBlob(ds, "2016-07-25.csv", ds+"/raw")
	s.ImportBlob(ds+"/raw", s.DatabaseSpec, "sf-reg-bus", "--dest-type", "map:0")
}

func (s *perfSuite) TestParseSfCrime() {
	assert := s.NewAssert()

	files := s.OpenGlob(path.Join(s.Testdata, "sf-crime", "2016-07-28.*"))
	defer s.CloseGlob(files)

	reader := csv.NewCSVReader(io.MultiReader(files...), ',')
	for {
		_, err := reader.Read()
		if err != nil {
			assert.Equal(io.EOF, err)
			break
		}
	}
}

func TestPerf(t *testing.T) {
	suite.Run("csv-import", t, &perfSuite{})
}

