package nsq

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"
)

var byteSpace = []byte(" ")
var byteNewLine = []byte("\n")

const (
	traceIDExtK     = "##trace_id"
	dispatchTagExtK = "##client_dispatch_tag"
)

var (
	errCommandArg = errors.New("command argument error")
)

// Command represents a command from a client to an NSQ daemon
type Command struct {
	Name   []byte
	Params [][]byte
	Body   []byte
}

// String returns the name and parameters of the Command
func (c *Command) String() string {
	if len(c.Params) > 0 {
		return fmt.Sprintf("%s %s", c.Name, string(bytes.Join(c.Params, byteSpace)))
	}
	return string(c.Name)
}

// WriteTo implements the WriterTo interface and
// serializes the Command to the supplied Writer.
//
// It is suggested that the target Writer is buffered
// to avoid performing many system calls.
func (c *Command) WriteTo(w io.Writer) (int64, error) {
	var total int64
	var buf [4]byte

	n, err := w.Write(c.Name)
	total += int64(n)
	if err != nil {
		return total, err
	}

	for _, param := range c.Params {
		n, err := w.Write(byteSpace)
		total += int64(n)
		if err != nil {
			return total, err
		}
		n, err = w.Write(param)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}

	n, err = w.Write(byteNewLine)
	total += int64(n)
	if err != nil {
		return total, err
	}

	if c.Body != nil {
		bufs := buf[:]
		binary.BigEndian.PutUint32(bufs, uint32(len(c.Body)))
		n, err := w.Write(bufs)
		total += int64(n)
		if err != nil {
			return total, err
		}
		n, err = w.Write(c.Body)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}

	return total, nil
}

// Identify creates a new Command to provide information about the client.  After connecting,
// it is generally the first message sent.
//
// The supplied map is marshaled into JSON to provide some flexibility
// for this command to evolve over time.
//
// See http://nsq.io/clients/tcp_protocol_spec.html#identify for information
// on the supported options
func Identify(js map[string]interface{}) (*Command, error) {
	body, err := json.Marshal(js)
	if err != nil {
		return nil, err
	}
	return &Command{[]byte("IDENTIFY"), nil, body}, nil
}

// Auth sends credentials for authentication
//
// After `Identify`, this is usually the first message sent, if auth is used.
func Auth(secret string) (*Command, error) {
	return &Command{[]byte("AUTH"), nil, []byte(secret)}, nil
}

// Register creates a new Command to add a topic/channel for the connected nsqd
func Register(topic string, partition string, channel string) *Command {
	params := [][]byte{[]byte(topic)}
	params = append(params, []byte(partition))
	if len(channel) > 0 {
		params = append(params, []byte(channel))
	}
	return &Command{[]byte("REGISTER"), params, nil}
}

// UnRegister creates a new Command to remove a topic/channel for the connected nsqd
func UnRegister(topic string, partition string, channel string) *Command {
	params := [][]byte{[]byte(topic)}
	params = append(params, []byte(partition))
	if len(channel) > 0 {
		params = append(params, []byte(channel))
	}
	return &Command{[]byte("UNREGISTER"), params, nil}
}

// Ping creates a new Command to keep-alive the state of all the
// announced topic/channels for a given client
func Ping() *Command {
	return &Command{[]byte("PING"), nil, nil}
}

// Publish creates a new Command to write a message to a given topic
func CreateTopic(topic string, partition int) *Command {
	var params = [][]byte{[]byte(topic), []byte(strconv.Itoa(partition)), []byte("false")}
	return &Command{[]byte("INTERNAL_CREATE_TOPIC"), params, nil}
}

func CreateTopicWithExt(topic string, partition int) *Command {
	var params = [][]byte{[]byte(topic), []byte(strconv.Itoa(partition)), []byte("true")}
	return &Command{[]byte("INTERNAL_CREATE_TOPIC"), params, nil}
}

// Publish creates a new Command to write a message to a given topic
func Publish(topic string, body []byte) *Command {
	var params = [][]byte{[]byte(topic)}
	return &Command{[]byte("PUB"), params, body}
}

// Publish creates a new Command to write a message to a given topic
func PublishWithPart(topic string, part string, body []byte) *Command {
	var params = [][]byte{[]byte(topic), []byte(part)}
	return &Command{[]byte("PUB"), params, body}
}

func PublishTrace(topic string, part string, traceID uint64, body []byte) (*Command, error) {
	var params = [][]byte{[]byte(topic), []byte(part)}
	buf := bytes.NewBuffer(make([]byte, 0, 8+len(body)))
	err := binary.Write(buf, binary.BigEndian, &traceID)
	if err != nil {
		return nil, err
	}
	_, err = buf.Write(body)
	if err != nil {
		return nil, err
	}
	return &Command{[]byte("PUB_TRACE"), params, buf.Bytes()}, nil
}

func PublishWithJsonExt(topic string, part string, body []byte, jsonExt []byte) (*Command, error) {
	var params = [][]byte{[]byte(topic), []byte(part)}
	if len(jsonExt) > 65535 {
		return nil, errCommandArg
	}
	hlen := uint16(len(jsonExt))
	extBody := make([]byte, len(body)+2+len(jsonExt))
	binary.BigEndian.PutUint16(extBody, hlen)
	copy(extBody[2:], jsonExt)
	copy(extBody[2+hlen:], body)
	return &Command{[]byte("PUB_EXT"), params, extBody}, nil
}

func getMPubBodyV2(bodies []*bytes.Buffer) (*bytes.Buffer, error) {
	num := uint32(len(bodies))
	bodySize := 4
	for _, b := range bodies {
		bodySize += b.Len() + 4
	}
	body := make([]byte, 0, bodySize)
	buf := bytes.NewBuffer(body)

	err := binary.Write(buf, binary.BigEndian, &num)
	if err != nil {
		return nil, err
	}
	for _, b := range bodies {
		err = binary.Write(buf, binary.BigEndian, int32(b.Len()))
		if err != nil {
			return nil, err
		}
		_, err = buf.Write(b.Bytes())
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func getMPubBody(bodies [][]byte) (*bytes.Buffer, error) {
	num := uint32(len(bodies))
	bodySize := 4
	for _, b := range bodies {
		bodySize += len(b) + 4
	}
	body := make([]byte, 0, bodySize)
	buf := bytes.NewBuffer(body)

	err := binary.Write(buf, binary.BigEndian, &num)
	if err != nil {
		return nil, err
	}
	for _, b := range bodies {
		err = binary.Write(buf, binary.BigEndian, int32(len(b)))
		if err != nil {
			return nil, err
		}
		_, err = buf.Write(b)
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func getMPubBodyWithJsonExt(extList []*MsgExt, bodies [][]byte) (*bytes.Buffer, error) {
	num := uint32(len(bodies))
	jsonExtBytesList := make([][]byte, num)
	bodySize := 4
	for i, b := range bodies {
		extJsonBytes := extList[i].ToJson();
		jsonExtBytesList[i] = extJsonBytes
		bodySize += len(b) + 4 + 2 + len(extJsonBytes)
	}
	body := make([]byte, 0, bodySize)
	buf := bytes.NewBuffer(body)

	err := binary.Write(buf, binary.BigEndian, &num)
	if err != nil {
		return nil, err
	}
	for i, b := range bodies {
		// the length should contain the body size + 8 bytes trace id.
		err = binary.Write(buf, binary.BigEndian, int32(len(b) + 2 + len(jsonExtBytesList[i])))
		if err != nil {
			return nil, err
		}
		err := binary.Write(buf, binary.BigEndian, int16(len(jsonExtBytesList[i])))
		if err != nil {
			return nil, err
		}

		_, err = buf.Write(jsonExtBytesList[i])
		if err != nil {
			return nil, err
		}

		_, err = buf.Write(b)
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func getMPubBodyForTrace(traceIDList []uint64, bodies [][]byte) (*bytes.Buffer, error) {
	num := uint32(len(bodies))
	bodySize := 4
	for _, b := range bodies {
		bodySize += len(b) + 4 + 8
	}
	body := make([]byte, 0, bodySize)
	buf := bytes.NewBuffer(body)

	err := binary.Write(buf, binary.BigEndian, &num)
	if err != nil {
		return nil, err
	}
	for i, b := range bodies {
		// the length should contain the body size + 8 bytes trace id.
		err = binary.Write(buf, binary.BigEndian, int32(len(b)+8))
		if err != nil {
			return nil, err
		}
		err := binary.Write(buf, binary.BigEndian, traceIDList[i])
		if err != nil {
			return nil, err
		}
		_, err = buf.Write(b)
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func MultiPublishV2(topic string, bodies []*bytes.Buffer) (*Command, error) {
	var params = [][]byte{[]byte(topic)}

	buf, err := getMPubBodyV2(bodies)
	if err != nil {
		return nil, err
	}
	return &Command{[]byte("MPUB"), params, buf.Bytes()}, nil
}

// MultiPublish creates a new Command to write more than one message to a given topic
// (useful for high-throughput situations to avoid roundtrips and saturate the pipe)
func MultiPublish(topic string, bodies [][]byte) (*Command, error) {
	var params = [][]byte{[]byte(topic)}

	buf, err := getMPubBody(bodies)
	if err != nil {
		return nil, err
	}
	return &Command{[]byte("MPUB"), params, buf.Bytes()}, nil
}

func MultiPublishWithPartV2(topic string, part string, bodies []*bytes.Buffer) (*Command, error) {
	var params = [][]byte{[]byte(topic), []byte(part)}

	buf, err := getMPubBodyV2(bodies)
	if err != nil {
		return nil, err
	}
	return &Command{[]byte("MPUB"), params, buf.Bytes()}, nil
}

// MultiPublish creates a new Command to write more than one message to a given topic
// (useful for high-throughput situations to avoid roundtrips and saturate the pipe)
func MultiPublishWithPart(topic string, part string, bodies [][]byte) (*Command, error) {
	var params = [][]byte{[]byte(topic), []byte(part)}

	buf, err := getMPubBody(bodies)
	if err != nil {
		return nil, err
	}
	return &Command{[]byte("MPUB"), params, buf.Bytes()}, nil
}

// MultiPublish creates a new Command to write more than one message to a given topic
// (useful for high-throughput situations to avoid roundtrips and saturate the pipe)
func MultiPublishTrace(topic string, part string, traceIDList []uint64, bodies [][]byte) (*Command, error) {
	if len(traceIDList) != len(bodies) {
		return nil, errCommandArg
	}
	var params = [][]byte{[]byte(topic), []byte(part)}
	buf, err := getMPubBodyForTrace(traceIDList, bodies)
	if err != nil {
		return nil, err
	}
	return &Command{[]byte("MPUB_TRACE"), params, buf.Bytes()}, nil
}

func MultiPublishWithJsonExt(topic string, part string, extList []*MsgExt, bodies [][]byte) (*Command, error) {
	if len(extList) != len(bodies) {
		return nil, errCommandArg
	}
	var params = [][]byte{[]byte(topic), []byte(part)}
	buf, err := getMPubBodyWithJsonExt(extList, bodies)
	if err != nil {
		return nil, err
	}
	return &Command{[]byte("MPUB_EXT"), params, buf.Bytes()}, nil
}

// Subscribe creates a new Command to subscribe to the given topic/channel
func Subscribe(topic string, channel string) *Command {
	var params = [][]byte{[]byte(topic), []byte(channel)}
	return &Command{[]byte("SUB"), params, nil}
}

func SubscribeWithPart(topic string, channel string, part string) *Command {
	var params = [][]byte{[]byte(topic), []byte(channel), []byte(part)}
	return &Command{[]byte("SUB"), params, nil}
}

func SubscribeAndTrace(topic string, channel string) *Command {
	var params = [][]byte{[]byte(topic), []byte(channel)}
	return &Command{[]byte("SUB_ADVANCED"), params, nil}
}

func SubscribeWithPartAndTrace(topic string, channel string, part string) *Command {
	var params = [][]byte{[]byte(topic), []byte(channel), []byte(part)}
	return &Command{[]byte("SUB_ADVANCED"), params, nil}
}

//var offsetCountType = "count"
var OffsetTimestampType = "timestamp"
var OffsetVirtualQueueType = "virtual_queue"
var OffsetSpecialType = "special"

type ConsumeOffset struct {
	OffsetType  string
	OffsetValue int64
}

//func (self *ConsumeOffset) SetCount(offset int64) {
//	self.OffsetType = offsetCountType
//	self.OffsetValue = offset
//}

func (self *ConsumeOffset) SetToEnd() {
	self.OffsetType = OffsetSpecialType
	self.OffsetValue = -1
}

func (self *ConsumeOffset) SetVirtualQueueOffset(offset int64) {
	self.OffsetType = OffsetVirtualQueueType
	self.OffsetValue = offset
}

// sub from the second since epoch time
func (self *ConsumeOffset) SetTime(sec int64) {
	self.OffsetType = OffsetTimestampType
	self.OffsetValue = sec
}

func (self *ConsumeOffset) ToString() string {
	if self.OffsetType == "" {
		return ""
	}
	return self.OffsetType + ":" + strconv.FormatInt(self.OffsetValue, 10)
}

func SubscribeOrdered(topic string, channel string, part string) *Command {
	var params = [][]byte{[]byte(topic), []byte(channel), []byte(part)}
	return &Command{[]byte("SUB_ORDERED"), params, nil}
}

func SubscribeAdvanced(topic string, channel string, part string, consumeStart ConsumeOffset) *Command {
	var params [][]byte
	params = [][]byte{[]byte(topic), []byte(channel), []byte(part), []byte(consumeStart.ToString())}
	return &Command{[]byte("SUB_ADVANCED"), params, nil}
}

// Ready creates a new Command to specify
// the number of messages a client is willing to receive
func Ready(count int) *Command {
	var params = [][]byte{[]byte(strconv.Itoa(count))}
	return &Command{[]byte("RDY"), params, nil}
}

// Finish creates a new Command to indiciate that
// a given message (by id) has been processed successfully
func Finish(id MessageID) *Command {
	var params = [][]byte{id[:]}
	return &Command{[]byte("FIN"), params, nil}
}

// Requeue creates a new Command to indicate that
// a given message (by id) should be requeued after the given delay
// NOTE: a delay of 0 indicates immediate requeue
func Requeue(id MessageID, delay time.Duration) *Command {
	var params = [][]byte{id[:], []byte(strconv.Itoa(int(delay / time.Millisecond)))}
	return &Command{[]byte("REQ"), params, nil}
}

// Touch creates a new Command to reset the timeout for
// a given message (by id)
func Touch(id MessageID) *Command {
	var params = [][]byte{id[:]}
	return &Command{[]byte("TOUCH"), params, nil}
}

// StartClose creates a new Command to indicate that the
// client would like to start a close cycle.  nsqd will no longer
// send messages to a client in this state and the client is expected
// finish pending messages and close the connection
func StartClose() *Command {
	return &Command{[]byte("CLS"), nil, nil}
}

// Nop creates a new Command that has no effect server side.
// Commonly used to respond to heartbeats
func Nop() *Command {
	return &Command{[]byte("NOP"), nil, nil}
}
