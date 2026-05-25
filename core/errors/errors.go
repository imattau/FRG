package errors

import "fmt"

type ErrorCode string

const (
	ErrArithmeticOverflow          ErrorCode = "ERR_001"
	ErrInvalidChildArity           ErrorCode = "ERR_002"
	ErrScaleDomainFault            ErrorCode = "ERR_003"
	ErrHashBoundaryMismatch        ErrorCode = "ERR_004"
	ErrMaskOutOfBounds             ErrorCode = "ERR_005"
	ErrPaddingSubstitutionFraud    ErrorCode = "ERR_006"
	ErrSignatureMisrepresentation  ErrorCode = "ERR_007"
	ErrNamespaceEscapeFault        ErrorCode = "ERR_008"
	ErrCanonicalEncodingDistortion ErrorCode = "ERR_009"
	ErrDosSizeExceeded             ErrorCode = "ERR_010"
	ErrRootMismatch                ErrorCode = "ERR_011"
)

type RGError struct {
	Code ErrorCode
	Msg  string
}

func (e *RGError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Msg == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

func New(code ErrorCode, msg string) *RGError {
	return &RGError{Code: code, Msg: msg}
}

func Newf(code ErrorCode, format string, args ...any) *RGError {
	return &RGError{Code: code, Msg: fmt.Sprintf(format, args...)}
}
