package config

const (
	UinputIM = iota
	PreeditIM
	SurroundingTextIM
	_ // BackspaceForwardingIM (removed)
	_ // ShiftLeftForwardingIM (removed)
	_ // ForwardAsCommitIM (removed)
	_ // XTestFakeKeyEventIM (removed)
	UsIM
)

var ImLookupTable = map[int]string{
	UinputIM:          "Kernel",
	PreeditIM:         "Preedit",
	SurroundingTextIM: "Surround",
	UsIM:              "Ignore",
}

var ImBackspaceList = []int{
	SurroundingTextIM,
}

const (
	IBautoCommitWithVnNotMatch uint = 1 << iota
	IBmacroEnabled
	_IBautoCommitWithVnFullMatch //deprecated
	_IBautoCommitWithVnWordBreak //deprecated
	IBspellCheckEnabled
	IBautoNonVnRestore
	IBddFreeStyle
	_IBnoUnderline // deprecated — PreeditIM always shows underline
	IBspellCheckWithRules
	IBspellCheckWithDicts
	IBautoCommitWithDelay
	_IBautoCommitWithMouseMovement //deprecated
	_IBemojiDisabled               //deprecated
	_IBpreeditElimination          //deprecated (was ForwardKeyEvent mode)
	_IBinputModeLookupTableEnabled //deprecated
	IBautoCapitalizeMacro
	_IBimQuickSwitchEnabled     //deprecated
	_IBrestoreKeyStrokesEnabled //deprecated
	_IBmouseCapturing           //deprecated
	_IBworkaroundForFBMessenger //deprecated — no longer used
	IBworkaroundForWPS
	IBdisableOnGame
	IBstdFlags = IBspellCheckEnabled | IBspellCheckWithRules | IBautoNonVnRestore | IBddFreeStyle |
		IBautoCapitalizeMacro | IBworkaroundForWPS | IBdisableOnGame
	IBUsStdFlags = IBmacroEnabled | IBautoCapitalizeMacro | IBdisableOnGame
)
