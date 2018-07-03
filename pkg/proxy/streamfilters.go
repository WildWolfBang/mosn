/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package proxy

import (
	"gitlab.alipay-inc.com/afe/mosn/pkg/network/buffer"
	"gitlab.alipay-inc.com/afe/mosn/pkg/types"
)

func (s *activeStream) addEncodedData(filter *activeStreamSenderFilter, data types.IoBuffer, streaming bool) {
	if s.filterStage == 0 || s.filterStage&EncodeHeaders > 0 ||
		s.filterStage&EncodeData > 0 {
		s.senderFiltersStreaming = streaming

		filter.handleBufferData(data)
	} else if s.filterStage&EncodeTrailers > 0 {
		s.runAppendDataFilters(filter, data, false)
	}
}

func (s *activeStream) addDecodedData(filter *activeStreamReceiverFilter, data types.IoBuffer, streaming bool) {
	if s.filterStage == 0 || s.filterStage&DecodeHeaders > 0 ||
		s.filterStage&DecodeData > 0 {
		s.receiverFiltersStreaming = streaming

		filter.handleBufferData(data)
	} else if s.filterStage&EncodeTrailers > 0 {
		s.decodeDataFilters(filter, data, false)
	}
}

func (s *activeStream) runAppendHeaderFilters(filter *activeStreamSenderFilter, headers interface{}, endStream bool) bool {
	var index int
	var f *activeStreamSenderFilter

	if filter != nil {
		index = filter.index + 1
	}

	for ; index < len(s.senderFilters); index++ {
		f = s.senderFilters[index]

		s.filterStage |= EncodeHeaders
		status := f.filter.AppendHeaders(headers, endStream)
		s.filterStage &= ^EncodeHeaders

		if status == types.FilterHeadersStatusStopIteration {
			f.stopped = true

			return true
		} else {
			f.headersContinued = true

			return false
		}
	}

	return false
}

func (s *activeStream) runAppendDataFilters(filter *activeStreamSenderFilter, data types.IoBuffer, endStream bool) bool {
	var index int
	var f *activeStreamSenderFilter

	if filter != nil {
		index = filter.index + 1
	}

	for ; index < len(s.senderFilters); index++ {
		f = s.senderFilters[index]

		s.filterStage |= EncodeData
		status := f.filter.AppendData(data, endStream)
		s.filterStage &= ^EncodeData

		if status == types.FilterDataStatusContinue {
			if f.stopped {
				f.handleBufferData(data)
				f.doContinue()

				return true
			}
		} else {
			f.stopped = true

			switch status {
			case types.FilterDataStatusStopIterationAndBuffer,
				types.FilterDataStatusStopIterationAndWatermark:
				s.senderFiltersStreaming = status == types.FilterDataStatusStopIterationAndWatermark
				f.handleBufferData(data)
			case types.FilterDataStatusStopIterationNoBuffer:
				f.stoppedNoBuf = true
				// make sure no data banked up
				data.Reset()
			}

			return true
		}
	}

	return false
}

func (s *activeStream) runAppendTrailersFilters(filter *activeStreamSenderFilter, trailers map[string]string) bool {
	var index int
	var f *activeStreamSenderFilter

	if filter != nil {
		index = filter.index + 1
	}

	for ; index < len(s.senderFilters); index++ {
		f = s.senderFilters[index]

		s.filterStage |= EncodeTrailers
		status := f.filter.AppendTrailers(trailers)
		s.filterStage &= ^EncodeTrailers

		if status == types.FilterTrailersStatusContinue {
			if f.stopped {
				f.doContinue()

				return true
			}
		} else {
			return true
		}
	}

	return false
}

func (s *activeStream) decodeHeaderFilters(filter *activeStreamReceiverFilter, headers map[string]string, endStream bool) bool {
	var index int
	var f *activeStreamReceiverFilter

	if filter != nil {
		index = filter.index + 1
	}

	for ; index < len(s.receiverFilters); index++ {
		f = s.receiverFilters[index]

		s.filterStage |= DecodeHeaders
		status := f.filter.OnDecodeHeaders(headers, endStream)
		s.filterStage &= ^DecodeHeaders

		if status == types.FilterHeadersStatusStopIteration {
			f.stopped = true

			return true
		} else {
			f.headersContinued = true

			return false
		}
	}

	return false
}

func (s *activeStream) decodeDataFilters(filter *activeStreamReceiverFilter, data types.IoBuffer, endStream bool) bool {
	if s.localProcessDone {
		return false
	}

	var index int
	var f *activeStreamReceiverFilter

	if filter != nil {
		index = filter.index + 1
	}

	for ; index < len(s.receiverFilters); index++ {
		f = s.receiverFilters[index]

		s.filterStage |= DecodeData
		status := f.filter.OnDecodeData(data, endStream)
		s.filterStage &= ^DecodeData

		if status == types.FilterDataStatusContinue {
			if f.stopped {
				f.handleBufferData(data)
				f.doContinue()

				return false
			}
		} else {
			f.stopped = true

			switch status {
			case types.FilterDataStatusStopIterationAndBuffer,
				types.FilterDataStatusStopIterationAndWatermark:
				s.receiverFiltersStreaming = status == types.FilterDataStatusStopIterationAndWatermark
				f.handleBufferData(data)
			case types.FilterDataStatusStopIterationNoBuffer:
				f.stoppedNoBuf = true
				// make sure no data banked up
				data.Reset()
			}

			return true
		}
	}

	return false
}

func (s *activeStream) decodeTrailersFilters(filter *activeStreamReceiverFilter, trailers map[string]string) bool {
	if s.localProcessDone {
		return false
	}

	var index int
	var f *activeStreamReceiverFilter

	if filter != nil {
		index = filter.index + 1
	}

	for ; index < len(s.receiverFilters); index++ {
		f = s.receiverFilters[index]

		s.filterStage |= DecodeTrailers
		status := f.filter.OnDecodeTrailers(trailers)
		s.filterStage &= ^DecodeTrailers

		if status == types.FilterTrailersStatusContinue {
			if f.stopped {
				f.doContinue()

				return false
			}
		} else {
			return true
		}
	}

	return false
}

type FilterStage int

const (
	DecodeHeaders = iota
	DecodeData
	DecodeTrailers
	EncodeHeaders
	EncodeData
	EncodeTrailers
)

// types.StreamFilterCallbacks
type activeStreamFilter struct {
	index int

	activeStream     *activeStream
	stopped          bool
	stoppedNoBuf     bool
	headersContinued bool
}

func (f *activeStreamFilter) Connection() types.Connection {
	return f.activeStream.proxy.readCallbacks.Connection()
}

func (f *activeStreamFilter) ResetStream() {
	f.activeStream.resetStream()
}

func (f *activeStreamFilter) Route() types.Route {
	return f.activeStream.route
}

func (f *activeStreamFilter) StreamId() string {
	return f.activeStream.streamId
}

func (f *activeStreamFilter) RequestInfo() types.RequestInfo {
	return f.activeStream.requestInfo
}

// types.StreamReceiverFilterCallbacks
type activeStreamReceiverFilter struct {
	activeStreamFilter

	filter types.StreamReceiverFilter
}

func newActiveStreamReceiverFilter(idx int, activeStream *activeStream,
	filter types.StreamReceiverFilter) *activeStreamReceiverFilter {
	f := &activeStreamReceiverFilter{
		activeStreamFilter: activeStreamFilter{
			index:        idx,
			activeStream: activeStream,
		},
		filter: filter,
	}
	filter.SetDecoderFilterCallbacks(f)

	return f
}

func (f *activeStreamReceiverFilter) ContinueDecoding() {
	f.doContinue()
}

func (f *activeStreamReceiverFilter) doContinue() {
	if f.activeStream.localProcessDone {
		return
	}

	f.stopped = false
	hasBuffedData := f.activeStream.downstreamReqDataBuf != nil
	hasTrailer := f.activeStream.downstreamReqTrailers != nil

	if !f.headersContinued {
		f.headersContinued = true

		endStream := f.activeStream.downstreamRecvDone && !hasBuffedData && !hasTrailer
		f.activeStream.doReceiveHeaders(f, f.activeStream.downstreamReqHeaders, endStream)
	}

	if hasBuffedData || f.stoppedNoBuf {
		if f.stoppedNoBuf || f.activeStream.downstreamReqDataBuf == nil {
			f.activeStream.downstreamReqDataBuf = buffer.NewIoBuffer(0)
		}

		endStream := f.activeStream.downstreamRecvDone && !hasTrailer
		f.activeStream.doReceiveData(f, f.activeStream.downstreamReqDataBuf, endStream)
	}

	if hasTrailer {
		f.activeStream.doReceiveTrailers(f, f.activeStream.downstreamReqTrailers)
	}
}

func (f *activeStreamReceiverFilter) handleBufferData(buf types.IoBuffer) {
	if f.activeStream.downstreamReqDataBuf != buf {
		if f.activeStream.downstreamReqDataBuf == nil {
			f.activeStream.downstreamReqDataBuf = buffer.NewIoBuffer(buf.Len())
		}

		f.activeStream.downstreamReqDataBuf.ReadFrom(buf)
	}
}

func (f *activeStreamReceiverFilter) DecodingBuffer() types.IoBuffer {
	return f.activeStream.downstreamReqDataBuf
}

func (f *activeStreamReceiverFilter) AddDecodedData(buf types.IoBuffer, streamingFilter bool) {
	f.activeStream.addDecodedData(f, buf, streamingFilter)
}

func (f *activeStreamReceiverFilter) AppendHeaders(headers interface{}, endStream bool) {
	f.activeStream.downstreamRespHeaders = headers
	f.activeStream.doAppendHeaders(nil, headers, endStream)
}

func (f *activeStreamReceiverFilter) AppendData(buf types.IoBuffer, endStream bool) {
	f.activeStream.doAppendData(nil, buf, endStream)
}

func (f *activeStreamReceiverFilter) AppendTrailers(trailers map[string]string) {
	f.activeStream.downstreamRespTrailers = trailers
	f.activeStream.doAppendTrailers(nil, trailers)
}

func (f *activeStreamReceiverFilter) SetDecoderBufferLimit(limit uint32) {
	f.activeStream.setBufferLimit(limit)
}

func (f *activeStreamReceiverFilter) DecoderBufferLimit() uint32 {
	return f.activeStream.bufferLimit
}

// types.StreamSenderFilterCallbacks
type activeStreamSenderFilter struct {
	activeStreamFilter

	filter types.StreamSenderFilter
}

func newActiveStreamSenderFilter(idx int, activeStream *activeStream,
	filter types.StreamSenderFilter) *activeStreamSenderFilter {
	f := &activeStreamSenderFilter{
		activeStreamFilter: activeStreamFilter{
			index:        idx,
			activeStream: activeStream,
		},
		filter: filter,
	}

	filter.SetEncoderFilterCallbacks(f)

	return f
}

func (f *activeStreamSenderFilter) ContinueEncoding() {
	f.doContinue()
}

func (f *activeStreamSenderFilter) doContinue() {
	f.stopped = false
	hasBuffedData := f.activeStream.downstreamRespDataBuf != nil
	hasTrailer := f.activeStream.downstreamRespTrailers == nil

	if !f.headersContinued {
		f.headersContinued = true
		endStream := f.activeStream.localProcessDone && !hasBuffedData && !hasTrailer
		f.activeStream.doAppendHeaders(f, f.activeStream.downstreamRespHeaders, endStream)
	}

	if hasBuffedData || f.stoppedNoBuf {
		if f.stoppedNoBuf || f.activeStream.downstreamRespDataBuf == nil {
			f.activeStream.downstreamRespDataBuf = buffer.NewIoBuffer(0)
		}

		endStream := f.activeStream.downstreamRecvDone && !hasTrailer
		f.activeStream.doAppendData(f, f.activeStream.downstreamRespDataBuf, endStream)
	}

	if hasTrailer {
		f.activeStream.doAppendTrailers(f, f.activeStream.downstreamRespTrailers)
	}
}

func (f *activeStreamSenderFilter) handleBufferData(buf types.IoBuffer) {
	if f.activeStream.downstreamRespDataBuf != buf {
		if f.activeStream.downstreamRespDataBuf == nil {
			f.activeStream.downstreamRespDataBuf = buffer.NewIoBuffer(buf.Len())
		}

		f.activeStream.downstreamRespDataBuf.ReadFrom(buf)
	}
}

func (f *activeStreamSenderFilter) EncodingBuffer() types.IoBuffer {
	return f.activeStream.downstreamRespDataBuf
}

func (f *activeStreamSenderFilter) AddEncodedData(buf types.IoBuffer, streamingFilter bool) {
	f.activeStream.addEncodedData(f, buf, streamingFilter)
}

func (f *activeStreamSenderFilter) SetEncoderBufferLimit(limit uint32) {
	f.activeStream.setBufferLimit(limit)
}

func (f *activeStreamSenderFilter) EncoderBufferLimit() uint32 {
	return f.activeStream.bufferLimit
}
