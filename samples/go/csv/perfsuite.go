// Copyright 2016 Attic Labs, Inc. All rights reserved.
// Licensed under the Apache License, version 2.0:
// http://www.apache.org/licenses/LICENSE-2.0

package csv

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/attic-labs/noms/go/perf/suite"
	"github.com/attic-labs/noms/go/types"
	"github.com/attic-labs/testify/assert"
	humanize "github.com/dustin/go-humanize"
)

// CSV perf suites require the testdata directory to be checked out at
// $GOPATH/src/github.com/attic-labs/testdata (i.e. ../testdata relative to the noms directory).

type CsvPerfSuite struct {
	suite.PerfSuite
	csvImportExe string
	csvExportExe string
	testDataset string
	testDataGlob string
}

func (s *CsvPerfSuite) SetupSuite() {
	// Trick the temp file logic into creating a unique path for the csv-export binary.
	f := s.TempFile()
	f.Close()
	os.Remove(f.Name())
	s.csvImportExe = f.Name()
	err := exec.Command("go", "build", "-o", s.csvImportExe, "github.com/attic-labs/noms/samples/go/csv/csv-import").Run()
	assert.NoError(s.T, err)

	f = s.TempFile()
	f.Close()
	os.Remove(f.Name())
	s.csvExportExe = f.Name()
	err = exec.Command("go", "build", "-o", s.csvExportExe, "github.com/attic-labs/noms/samples/go/csv/csv-export").Run()
	assert.NoError(s.T, err)
}

func (s *CsvPerfSuite) TearDownSuite() {
	os.Remove(s.csvExportExe)
	os.Remove(s.csvImportExe)
}


// load dataset
func (s *CsvPerfSuite) ImportDataToBlob(dsName string, glob string, blobName string) {
	assert := s.NewAssert()
	files := s.OpenGlob(s.Testdata, dsName, glob)
	defer s.CloseGlob(files)
	assert.NotEmpty(files, "no files match %s/%s", dsName, glob)

	blob := types.NewBlob(files...)
	fmt.Fprintf(s.W, "\t%s is %s\n", dsName, humanize.Bytes(blob.Len()))
	ds := s.Database.GetDataset(blobName)
	_, err := s.Database.CommitValue(ds, blob)
	assert.NoError(err)
}

func (s *CsvPerfSuite) ImportBlob(blobName, destDb, destDs string, args ...string) {
	blobValueSpec := fmt.Sprintf("%s::%s.value", s.DatabaseSpec, blobName)
	args = append(args, "-p", blobValueSpec)
	s.ExecImport(destDb, destDs, args...)
}

func (s *CsvPerfSuite) ExecImport(dbSpec, dsName string, args ...string) {
	assert := s.NewAssert()
	args = append(args, dbSpec+"::"+dsName)
	fmt.Fprintf(s.W, "import(%v)\n", args)
	importCmd := exec.Command(s.csvImportExe, args...)
	importCmd.Stdout = s.W
	importCmd.Stderr = os.Stderr
	assert.NoError(importCmd.Run())
}


func (s *CsvPerfSuite) ExecExport(dbSpec, dsName string, output io.Writer, args ...string) {
	assert := s.NewAssert()
	args = append(args, dbSpec+"::"+dsName)
	fmt.Fprintf(s.W, "export(%s %v)\n", s.csvExportExe, args)
	exportCmd := exec.Command(s.csvExportExe, args...)
	exportCmd.Stdout = output
	exportCmd.Stderr = os.Stderr
	assert.NoError(exportCmd.Run())
}
