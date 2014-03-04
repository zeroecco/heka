/***** BEGIN LICENSE BLOCK *****
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this file,
# You can obtain one at http://mozilla.org/MPL/2.0/.
#
# The Initial Developer of the Original Code is the Mozilla Foundation.
# Portions created by the Initial Developer are Copyright (C) 2012
# the Initial Developer. All Rights Reserved.
#
# Contributor(s):
#   Ben Bangert (bbangert@mozilla.com)
#
# ***** END LICENSE BLOCK *****/

package logstreamer

import (
	"code.google.com/p/gomock/gomock"
	. "github.com/mozilla-services/heka/pipeline"
	pipeline_ts "github.com/mozilla-services/heka/pipeline/testsupport"
	"github.com/mozilla-services/heka/pipelinemock"
	plugins_ts "github.com/mozilla-services/heka/plugins/testsupport"
	gs "github.com/rafrombrc/gospec/src/gospec"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAllSpecs(t *testing.T) {
	r := gs.NewRunner()
	r.Parallel = false

	r.AddSpec(LogstreamerInputSpec)

	gs.MainGoTest(r, t)
}

func LogstreamerInputSpec(c gs.Context) {
	t := &pipeline_ts.SimpleT{}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	here, _ := os.Getwd()
	dirPath := filepath.Join(here, "../../logstreamer", "testdir", "filehandling/subdir")

	config := NewPipelineConfig(nil)
	ith := new(plugins_ts.InputTestHelper)
	ith.Msg = pipeline_ts.GetTestMessage()
	ith.Pack = NewPipelinePack(config.InputRecycleChan())

	// Specify localhost, but we're not really going to use the network
	ith.AddrStr = "localhost:55565"
	ith.ResolvedAddrStr = "127.0.0.1:55565"

	// set up mock helper, decoder set, and packSupply channel
	ith.MockHelper = pipelinemock.NewMockPluginHelper(ctrl)
	ith.MockInputRunner = pipelinemock.NewMockInputRunner(ctrl)
	ith.Decoder = pipelinemock.NewMockDecoderRunner(ctrl)
	ith.PackSupply = make(chan *PipelinePack, 1)
	ith.DecodeChan = make(chan *PipelinePack)

	c.Specify("A LogFileInput", func() {
		tmpDir, tmpErr := ioutil.TempDir("", "hekad-tests-")
		c.Expect(tmpErr, gs.Equals, nil)
		origBaseDir := Globals().BaseDir
		Globals().BaseDir = tmpDir
		defer func() {
			Globals().BaseDir = origBaseDir
			tmpErr = os.RemoveAll(tmpDir)
			c.Expect(tmpErr, gs.IsNil)
		}()
		lfInput := new(LogstreamerInput)
		lfiConfig := lfInput.ConfigStruct().(*LogstreamerInputConfig)
		lfiConfig.LogDirectory = dirPath
		lfiConfig.FileMatch = `file.log(\.?)(?P<Seq>\d+)?`
		lfiConfig.Differentiator = []string{"logfile"}
		lfiConfig.Priority = []string{"^Seq"}
		lfiConfig.Decoder = "decoder-name"
		err := lfInput.Init(lfiConfig)
		c.Expect(err, gs.IsNil)
		c.Expect(len(lfInput.plugins), gs.Equals, 1)
		mockDecoderRunner := pipelinemock.NewMockDecoderRunner(ctrl)

		// Create pool of packs.
		numLines := 5 // # of lines in the log file we're parsing.
		packs := make([]*PipelinePack, numLines)
		ith.PackSupply = make(chan *PipelinePack, numLines)
		for i := 0; i < numLines; i++ {
			packs[i] = NewPipelinePack(ith.PackSupply)
			ith.PackSupply <- packs[i]
		}

		c.Specify("reads a log file", func() {
			// Expect InputRunner calls to get InChan and inject outgoing msgs
			ith.MockInputRunner.EXPECT().LogError(gomock.Any()).AnyTimes()
			ith.MockInputRunner.EXPECT().LogMessage(gomock.Any()).AnyTimes()
			ith.MockInputRunner.EXPECT().InChan().Return(ith.PackSupply).Times(numLines)
			// Expect calls to get decoder and decode each message. Since the
			// decoding is a no-op, the message payload will be the log file
			// line, unchanged.
			pbcall := ith.MockHelper.EXPECT().DecoderRunner(lfiConfig.Decoder, "-"+lfiConfig.Decoder)
			pbcall.Return(mockDecoderRunner, true)
			decodeCall := mockDecoderRunner.EXPECT().InChan().Times(numLines)
			decodeCall.Return(ith.DecodeChan)

			go func() {
				err = lfInput.Run(ith.MockInputRunner, ith.MockHelper)
				c.Expect(err, gs.IsNil)
			}()

			d, _ := time.ParseDuration("5s")
			timeout := time.After(d)
			timed := false
			for x := 0; x < numLines; x++ {
				select {
				case <-ith.DecodeChan:
				case <-timeout:
					timed = true
					x += numLines
				}
				// Free up the scheduler while we wait for the log file lines
				// to be processed.
				runtime.Gosched()
			}
			lfInput.Stop()
			c.Expect(timed, gs.Equals, false)
		})
	})
}