// Copyright 2016 Attic Labs, Inc. All rights reserved.
// Licensed under the Apache License, version 2.0:
// http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"github.com/attic-labs/noms/go/perf/suite"
	"github.com/attic-labs/noms/samples/go/csv"
)

// CSV perf suites require the testdata directory to be checked out at
// $GOPATH/src/github.com/attic-labs/testdata (i.e. ../testdata relative to the noms directory).

type perfSuite struct {
	csv.CsvPerfSuite
	ldbSpec string
}

type data struct {
	ds   string
	glob string
}

var (
	SfFilms = data{ "sf-film-locations", "2016-*.*" }
	SfBusiness = data{ "sf-registered-businesses", "2016-07-25.csv" }
)

func (s *perfSuite) SetupSuite() {
	s.CsvPerfSuite.SetupSuite()
}

func (s *perfSuite) TeardownSuite() {
	s.CsvPerfSuite.TearDownSuite()
}

func (s *perfSuite) SetupRep() {
	// load all data into http and ldb db's
	s.ldbSpec = "ldb:" + s.TempDir()
	for _, d := range []data{SfFilms, SfBusiness } {
		blob := d.ds + "/raw"
		for _, db := range []string{ s.ldbSpec, s.DatabaseSpec } {
			s.ImportDataToBlob(d.ds, d.glob, blob)
			s.ImportBlob(blob, db, d.ds)
		}
	}
}

func (s *perfSuite) TeardownRep() {
	file := strings.TrimPrefix(s.ldbSpec, "ldb:")
	os.Remove(file)
}

func (s *perfSuite) Test01ExportSfFilmFromLdbToCSV() {
	s.ExecExport(s.ldbSpec, SfFilms.ds, ioutil.Discard)
}

func (s *perfSuite) Test02ExportSfFilmFromHttpToCSV() {
	s.ExecExport(s.DatabaseSpec, SfFilms.ds, ioutil.Discard)
}

func (s *perfSuite) Test03ExportSfBuisinessFromLdbToCSV() {
	s.ExecExport(s.ldbSpec, SfBusiness.ds, ioutil.Discard)
}

func (s *perfSuite) Test04ExportSfBusinessFromHttpToCSV() {
	s.ExecExport(s.DatabaseSpec, SfBusiness.ds, ioutil.Discard)
}

func TestPerf(t *testing.T) {
	suite.Run("csv-export", t, &perfSuite{})
}
