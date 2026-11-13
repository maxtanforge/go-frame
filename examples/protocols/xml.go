package protocols

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"reflect"

	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/validation/tags"
)

// Message is deliberately a partial business callback DTO. Identifiers remain
// strings; XML protocol values are not coerced through float64 or JSON maps.
type Message struct {
	XMLName xml.Name `xml:"xml" json:"-"`
	Type    string   `xml:"MsgType" json:"msg_type" v:"required" label:"消息类型"`
	ID      string   `xml:"MsgId" json:"msg_id"`
	Content string   `xml:"Content" json:"content"`
}

// NewXMLCallback requires business verification explicitly. This is plaintext
// callback wiring, not a WeChat signature/encryption implementation. An encrypted
// protocol must verify/decrypt its envelope before decoding the inner message.
func NewXMLCallback(verify func(context.Context, *http.Request, []byte) error, handle func(context.Context, Message) (string, error)) (http.Handler, error) {
	if verify == nil || handle == nil {
		return nil, errors.New("XML callback requires verification and handler")
	}
	rules, err := tags.Prepare(reflect.TypeFor[Message]())
	if err != nil {
		return nil, err
	}
	return respond.Adapter(func(w http.ResponseWriter, r *http.Request) (any, error) {
		raw, err := request.ReadXML(w, r)
		if err != nil {
			return nil, err
		}
		if err = verify(r.Context(), r, raw); err != nil {
			return nil, err
		}
		message, err := request.DecodeXMLBytes[Message](r.Context(), raw)
		if err != nil {
			return nil, err
		}
		if err = rules.Validate(&message, nil); err != nil {
			return nil, err
		}
		reply, err := handle(r.Context(), message)
		if err != nil {
			return nil, err
		}
		return nil, respond.Text(w, r, http.StatusOK, reply)
	}), nil
}
