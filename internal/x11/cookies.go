package x11

import (
	"github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

type InternAtomCookie interface {
	Reply() (*xproto.InternAtomReply, error)
}

type GetPropertyCookie interface {
	Reply() (*xproto.GetPropertyReply, error)
}

type GetGeometryCookie interface {
	Reply() (*xproto.GetGeometryReply, error)
}

type TranslateCoordinatesCookie interface {
	Reply() (*xproto.TranslateCoordinatesReply, error)
}

type SendEventCookie interface {
	Check() error
}

type MapWindowCookie interface {
	Check() error
}

type ConfigureWindowCookie interface {
	Check() error
}

type GetImageCookie interface {
	Reply() (*xproto.GetImageReply, error)
}

type FreePixmapCookie interface {
	Check() error
}

type NameWindowPixmapCookie interface {
	Check() error
}

// GetKeyboardMappingCookie wraps xproto.GetKeyboardMappingCookie
type GetKeyboardMappingCookie interface {
	Reply() (*xproto.GetKeyboardMappingReply, error)
}

// XTestFakeInputCookie wraps xtest.FakeInputCookie
type XTestFakeInputCookie interface {
	Check() error
}

// XProtoInternAtomCookie encapsulates xproto.InternAtomCookie
type XProtoInternAtomCookie struct {
	cookie xproto.InternAtomCookie
}

func NewXProtoInternAtomCookie(c xproto.InternAtomCookie) *XProtoInternAtomCookie {
	return &XProtoInternAtomCookie{cookie: c}
}

func (c *XProtoInternAtomCookie) Reply() (*xproto.InternAtomReply, error) {
	return c.cookie.Reply()
}

// XProtoGetPropertyCookie encapsulates xproto.GetPropertyCookie
type XProtoGetPropertyCookie struct {
	cookie xproto.GetPropertyCookie
}

func NewXProtoGetPropertyCookie(c xproto.GetPropertyCookie) *XProtoGetPropertyCookie {
	return &XProtoGetPropertyCookie{cookie: c}
}

func (c *XProtoGetPropertyCookie) Reply() (*xproto.GetPropertyReply, error) {
	return c.cookie.Reply()
}

// XProtoGetGeometryCookie encapsulates xproto.GetGeometryCookie
type XProtoGetGeometryCookie struct {
	cookie xproto.GetGeometryCookie
}

func NewXProtoGetGeometryCookie(c xproto.GetGeometryCookie) *XProtoGetGeometryCookie {
	return &XProtoGetGeometryCookie{cookie: c}
}

func (c *XProtoGetGeometryCookie) Reply() (*xproto.GetGeometryReply, error) {
	return c.cookie.Reply()
}

// XProtoTranslateCoordinatesCookie encapsulates xproto.TranslateCoordinatesCookie
type XProtoTranslateCoordinatesCookie struct {
	cookie xproto.TranslateCoordinatesCookie
}

func NewXProtoTranslateCoordinatesCookie(c xproto.TranslateCoordinatesCookie) *XProtoTranslateCoordinatesCookie {
	return &XProtoTranslateCoordinatesCookie{cookie: c}
}

func (c *XProtoTranslateCoordinatesCookie) Reply() (*xproto.TranslateCoordinatesReply, error) {
	return c.cookie.Reply()
}

// XProtoSendEventCookie encapsulates xproto.SendEventCookie
type XProtoSendEventCookie struct {
	cookie xproto.SendEventCookie
}

func NewXProtoSendEventCookie(c xproto.SendEventCookie) *XProtoSendEventCookie {
	return &XProtoSendEventCookie{cookie: c}
}

func (c *XProtoSendEventCookie) Check() error {
	return c.cookie.Check()
}

// XProtoMapWindowCookie encapsulates xproto.MapWindowCookie
type XProtoMapWindowCookie struct {
	cookie xproto.MapWindowCookie
}

func NewXProtoMapWindowCookie(c xproto.MapWindowCookie) *XProtoMapWindowCookie {
	return &XProtoMapWindowCookie{cookie: c}
}

func (c *XProtoMapWindowCookie) Check() error {
	return c.cookie.Check()
}

// XProtoConfigureWindowCookie encapsulates xproto.ConfigureWindowCookie
type XProtoConfigureWindowCookie struct {
	cookie xproto.ConfigureWindowCookie
}

func NewXProtoConfigureWindowCookie(c xproto.ConfigureWindowCookie) *XProtoConfigureWindowCookie {
	return &XProtoConfigureWindowCookie{cookie: c}
}

func (c *XProtoConfigureWindowCookie) Check() error {
	return c.cookie.Check()
}

type XProtoGetImageCookie struct {
	cookie xproto.GetImageCookie
}

func NewXProtoGetImageCookie(c xproto.GetImageCookie) *XProtoGetImageCookie {
	return &XProtoGetImageCookie{cookie: c}
}

func (c *XProtoGetImageCookie) Reply() (*xproto.GetImageReply, error) {
	return c.cookie.Reply()
}

type XProtoFreePixmapCookie struct {
	cookie xproto.FreePixmapCookie
}

func NewXProtoFreePixmapCookie(c xproto.FreePixmapCookie) *XProtoFreePixmapCookie {
	return &XProtoFreePixmapCookie{cookie: c}
}

func (c *XProtoFreePixmapCookie) Check() error {
	return c.cookie.Check()
}

type XProtoNameWindowPixmapCookie struct {
	cookie composite.NameWindowPixmapCookie
}

func NewXProtoNameWindowPixmapCookie(c composite.NameWindowPixmapCookie) *XProtoNameWindowPixmapCookie {
	return &XProtoNameWindowPixmapCookie{cookie: c}
}

func (c *XProtoNameWindowPixmapCookie) Check() error {
	return c.cookie.Check()
}

// GetKeyboardMapping wrapper
type XProtoGetKeyboardMappingCookie struct {
	cookie xproto.GetKeyboardMappingCookie
}

func NewXProtoGetKeyboardMappingCookie(c xproto.GetKeyboardMappingCookie) *XProtoGetKeyboardMappingCookie {
	return &XProtoGetKeyboardMappingCookie{cookie: c}
}

func (c *XProtoGetKeyboardMappingCookie) Reply() (*xproto.GetKeyboardMappingReply, error) {
	return c.cookie.Reply()
}

// XTest fake-input wrapper
type XProtoXTestFakeInputCookie struct {
	cookie xtest.FakeInputCookie
}

func NewXProtoXTestFakeInputCookie(c xtest.FakeInputCookie) *XProtoXTestFakeInputCookie {
	return &XProtoXTestFakeInputCookie{cookie: c}
}

func (c *XProtoXTestFakeInputCookie) Check() error {
	return c.cookie.Check()
}
