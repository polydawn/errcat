/*
	errcat is a simple universal error type that helps you produce
	errors that are both easy to categorize and handle, and also easy
	to maintain the original messages of.

	errcat does this by separating the two major parts of an error:
	the category and the message.

	The category is a value which you can switch on.
	*It is expected that the category field may be reassigned* as
	the error propagates up the stack.

	The message is the human-readable description of the error that occured.
	It *may* be further prepended with additional context info
	as it propagates out... or, not.
	The message may be redundant with the category: it is expected that
	the message will be printed to a user, while the category will
	not necessarily reach the user (it may be consumed by another layer
	of code, which may choose to re-categorize the error on its way up).

	Additional "details" may be attached in the Error.Details field;
	sometimes this can be used to provide key-value pairs which are
	useful in logging for other remote systems which must handle errors.
	However, usage of this should be minimized unless good reason is known;
	all handling logic should branch primarily on the category field,
	because that's what it's there for.

	errcat is specifically designed to be *serializable*, and just as
	importantly, *unserializable* again.
	This is helpful for making API-driven applications with
	consistent and reliably round-trip-able errors.
	errcat errors in json should appear as a very simple object:

		{"category":"your_tag", "msg":"full text goes here"}

	If details are present, they're an additional map[string]string:

		{"category":"your_tag", "msg":"full text", "details":{"foo":"bar"}}

	Typical usage patterns involve a const block in each package which
	enumerates the set of error category values that this package may return.
	When calling functions using the errcat convention, the callers may
	switch upon the returned Error's Category property:

		result, err := somepkg.SomeFunc()
		switch errcat.Category(err) {
		case nil:
			// good!  pass!
		case somepkg.ErrAlreadyDone:
			// good!  pass!
		case somepkg.ErrDataCorruption:
			// ... handle ...
		default:
			panic("bug: unknown error category")
		}

	Use the public functions of this package to create errors,
	and accessor functions (like 'errcat.Category' for example) to access
	the properties.
	All your code should use the stdlib `error` interface and these package functions.
	Using the interfaces rather than a concrete type means you (or others)
	can easily vendor this library even under different import paths,
	and all of your error types will interact correctly.
	Prefering the `error` type to the `errcat.Error` interface avoids common
	developer irritants that may otherwise arrise from type specificity
	when putting both types into a variable named "err";
	all of the errcat package funcs both take and return `error` interfaces
	for this reason.

	Functions internal to packages may chose to panic up their errors.
	It is idiomatic to recover such internal panics and return the error
	as normal at the top of the package even when using panics as a
	non-local return system internally.
*/
package errcat

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

type Error interface {
	Category() interface{}      // The category value.  Must be serializable as a string.  Any programatic error handling should switch on this field (exclusively!).
	Message() string            // A human-readable message to print.
	Details() map[string]string // A map of optional "details".
	error                       // Errcat error interfaces are also always stdlib errors.  The `Error() string` method typically aliases `Message() string`.
}

//
// The concrete type
//       ...
//

var _ Error = &errStruct{}

type errStruct struct {
	Category_ interface{}       `json:"category"          refmt:"category"`
	Message_  string            `json:"message"           refmt:"message"`
	Details_  map[string]string `json:"details,omitempty" refmt:"details,omitempty"`
}

func (e *errStruct) Category() interface{}      { return e.Category_ }
func (e *errStruct) Message() string            { return e.Message_ }
func (e *errStruct) Details() map[string]string { return e.Details_ }
func (e *errStruct) Error() string              { return e.Message_ }

//
// Factories
//    ...
//

/*
	Return a new error with the given category, and a message composed of
	`fmt.Sprintf`'ing the remaining arguments.
*/
func Errorf(category interface{}, format string, args ...interface{}) error {
	return &errStruct{category, fmt.Sprintf(format, args...), nil}
}

/*
	Return a new error with the same message and details of the given error
	and a category assigned to the new value.

	If the given error is nil, nil will be returned.
*/
func Recategorize(category interface{}, err error) error {
	switch e2 := err.(type) {
	case nil:
		return nil
	case Error:
		return &errStruct{category, e2.Message(), e2.Details()}
	default:
		return &errStruct{category, e2.Error(), nil}
	}
}

/*
	Return a new error with the given category, message, and details map.
*/
func ErrorDetailed(category interface{}, msg string, details map[string]string) error {
	return &errStruct{category, msg, details}
}

/*
	Return a new error with the same category and message, and the given k-v pair
	of details appended.

	Nil errors will be passed through.
	Non-errcat errors are also passed through; the details will be lost (caveat
	emptor; do not use this method if you haven't already normalized your errors
	into errcat form).
*/
func AppendDetail(err error, key string, value string) error {
	switch e2 := err.(type) {
	case nil:
		return nil
	case Error:
		d2 := make(map[string]string, len(e2.Details()))
		for k, v := range e2.Details() {
			d2[k] = v
		}
		d2[key] = value
		return &errStruct{e2.Category(), e2.Message(), d2}
	default:
		return err
	}
}

func PrefixAnnotate(err error, msg string, details [][2]string) error {
	switch e2 := err.(type) {
	case nil:
		return nil
	case Error:
		t := template.New("").Funcs(template.FuncMap{
			"join":  strings.Join,
			"quote": func(x interface{}) string { return fmt.Sprintf("%q", x) },
		})
		t, err := t.Parse(msg)
		var buf bytes.Buffer
		if err != nil {
			buf.WriteString(fmt.Sprintf("[[%s]]", err))
		}
		if t != nil {
			data := make(map[string]string, len(details))
			for _, v := range details {
				data[v[0]] = v[1]
			}
			err = t.Execute(&buf, data)
			if err != nil {
				buf.WriteString(fmt.Sprintf("[[%s]]", err))
			}
		}

		d2 := make(map[string]string, len(e2.Details()))
		for k, v := range e2.Details() {
			d2[k] = v
		}
		for _, v := range details {
			d2[v[0]] = v[1]
		}

		return &errStruct{e2.Category(), buf.String() + ": " + e2.Message(), d2}
	default:
		return err
	}
}

//
// Accessors
//    ...
//

/*
	Return the value of `err.(errcat.Error).Category()` if that typecast works,
	or the sentinel value `errcat.unknown` if the typecast fails,
	or nil if the error is nil.

	This is useful for switching on the category of an error, even when
	functions declare that they return the broader `error` interface,
	like so:

		result, err := somepkg.SomeFunc()
		switch errcat.Category(err) {
		case nil:
			// good!  pass!
		case somepkg.ErrAlreadyDone:
			// good!  pass!
		case somepkg.ErrDataCorruption:
			// ... handle ...
		default:
			panic("bug: unknown error category")
		}
*/
func Category(err error) interface{} {
	if err == nil {
		return nil
	}
	e, ok := err.(Error)
	if !ok {
		return unknown
	}
	return e.Category()
}

/*
	Return the value of `err.(errcat.Error).Details()` if that typecast works,
	or nil if the typecast fails,
	or nil if the error is nil.
*/
func Details(err error) map[string]string {
	if err == nil {
		return nil
	}
	e, ok := err.(Error)
	if !ok {
		return nil
	}
	return e.Details()
}

// our internal error categories.  callers should never have a need to reference them.
type errorCategory string

const unknown = errorCategory("unknown-category") // sentinel value for Category() to return on non-errcat errors.
