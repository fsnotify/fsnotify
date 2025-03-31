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

package goleak

import (
	"fmt"
	"io"
	"os"
)

// Variables for stubbing in unit tests.
var (
	_osExit             = os.Exit
	_osStderr io.Writer = os.Stderr
)

// testingM is the minimal subset of testing.M that we use.
type testingM interface {
	Run() int
}

// VerifyTestMain can be used in a TestMain function for package tests to
// verify that there were no goroutine leaks.
// To use it, your TestMain function should look like:
//
//	func TestMain(m *testing.M) {
//	  goleak.VerifyTestMain(m)
//	}
//
// See https://pkg.go.dev/testing#hdr-Main for more details.
//
// This will run all tests as per normal, and if they were successful, look
// for any goroutine leaks and fail the tests if any leaks were found.
func VerifyTestMain(m testingM, options ...Option) {
	exitCode := m.Run()
	opts := buildOpts(options...)

	var cleanup func(int)
	cleanup, opts.cleanup = opts.cleanup, nil
	if cleanup == nil {
		cleanup = _osExit
	}
	defer func() { cleanup(exitCode) }()

	var (
		run      bool
		errorMsg string
	)

	if !opts.runOnFailure && exitCode == 0 {
		errorMsg = "goleak: Errors on successful test run:%v\n"
		run = true
	} else if opts.runOnFailure {
		errorMsg = "goleak: Errors on unsuccessful test run: %v\n"
		run = true
	}

	if run {
		if err := find(opts); err != nil {
			fmt.Fprintf(_osStderr, errorMsg, err)
			// rewrite exitCode if test passed and is set to 0.
			if exitCode == 0 {
				exitCode = 1
			}
		}
	}
}
