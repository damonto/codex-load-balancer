package plus

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

const turnstileAuthURL = "https://auth.openai.com/create-account/password"

var turnstileLocalStorageKeys = []string{
	"d2bd098fc8793ef1",
	"statsig.session_id.444584300",
	"statsig.last_modified_time.evaluations",
	"statsig.stable_id.444584300",
	"6fbbfe1cd1015f3d",
	"statsig.cached.evaluations.3523433505",
}

type turnstileProofConfig struct {
	PerfNow             float64
	TimeOrigin          float64
	HardwareConcurrency int
	Screen              turnstileScreen
	LocalStorageKeys    []string
}

type turnstileScreen struct {
	AvailWidth  int
	AvailHeight int
	AvailLeft   int
	AvailTop    int
	ColorDepth  int
	Height      int
	Width       int
	PixelDepth  int
}

type turnstileSolver struct {
	ctx          context.Context
	dx           string
	proof        string
	cfg          turnstileProofConfig
	rng          *turnstileRNG
	program      map[float64]string
	registers    map[float64]any
	pc           int
	localStorage map[string]string
}

type turnstileRNG struct {
	state uint64
}

func newTurnstileSolver(ctx context.Context, dx, proof string) (*turnstileSolver, error) {
	cfg, err := decodeTurnstileProof(proof)
	if err != nil {
		return nil, fmt.Errorf("decode turnstile proof: %w", err)
	}

	localStorage := make(map[string]string, len(cfg.LocalStorageKeys))
	for _, key := range cfg.LocalStorageKeys {
		localStorage[key] = ""
	}

	return &turnstileSolver{
		ctx:          ctx,
		dx:           dx,
		proof:        proof,
		cfg:          cfg,
		rng:          newTurnstileRNG(proof + "\x00" + dx),
		program:      newTurnstileProgram(),
		registers:    make(map[float64]any),
		localStorage: localStorage,
	}, nil
}

func newTurnstileProgram() map[float64]string {
	return map[float64]string{
		0:  "LOAD_PROGRAM",
		1:  "XOR_REG",
		2:  "SET",
		3:  "RESOLVE",
		4:  "REJECT",
		5:  "ADD",
		6:  "GET_INDEX",
		7:  "CALL",
		8:  "COPY",
		9:  "IP",
		10: "window",
		11: "SCRIPT_MATCH",
		12: "GET_VM",
		13: "TRY_CALL",
		14: "JSON_PARSE",
		15: "JSON_STRINGIFY",
		16: "KEY",
		17: "TRY_CALL_RESULT",
		18: "ATOB",
		19: "BTOA",
		20: "IF_EQUAL",
		21: "IF_NOT_CLOSE",
		22: "SUBROUTINE",
		23: "IF_DEFINED",
		24: "BIND",
		27: "SUB",
		29: "LESS_THAN",
		30: "DEFINE_FUNC",
		33: "MULTIPLY",
		34: "AWAIT",
		35: "DIVIDE",
	}
}

func newTurnstileRNG(seed string) *turnstileRNG {
	sum := sha256.Sum256([]byte(seed))
	state := binary.LittleEndian.Uint64(sum[:8])
	if state == 0 {
		state = 0x9e3779b97f4a7c15
	}
	return &turnstileRNG{state: state}
}

func (r *turnstileRNG) Float64() float64 {
	r.state ^= r.state >> 12
	r.state ^= r.state << 25
	r.state ^= r.state >> 27
	value := r.state * 2685821657736338717
	return float64(value>>11) / float64(uint64(1)<<53)
}

func decodeTurnstileProof(proof string) (turnstileProofConfig, error) {
	trimmed := strings.TrimSpace(proof)
	trimmed = strings.TrimSuffix(trimmed, "~S")
	trimmed = strings.TrimPrefix(trimmed, "gAAAAAC")
	trimmed = strings.TrimPrefix(trimmed, "gAAAAAB")
	if trimmed == "" {
		return turnstileProofConfig{}, errors.New("turnstile proof payload is empty")
	}

	raw, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return turnstileProofConfig{}, fmt.Errorf("base64 decode proof payload: %w", err)
	}

	var items []any
	if err := json.Unmarshal(raw, &items); err != nil {
		return turnstileProofConfig{}, fmt.Errorf("parse proof payload: %w", err)
	}

	cfg := turnstileProofConfig{
		PerfNow:             24594.19999998808,
		TimeOrigin:          1775204954422.1,
		HardwareConcurrency: 8,
		Screen: turnstileScreen{
			Width:       1512,
			Height:      928,
			AvailWidth:  1512,
			AvailHeight: 898,
			AvailLeft:   0,
			AvailTop:    0,
			ColorDepth:  24,
			PixelDepth:  24,
		},
		LocalStorageKeys: append([]string(nil), turnstileLocalStorageKeys...),
	}

	if len(items) > 0 {
		if value, ok := items[0].(string); ok {
			parts := strings.SplitN(value, "x", 2)
			if len(parts) == 2 {
				width := int(toFloat(parts[0]))
				height := int(toFloat(parts[1]))
				if width > 0 {
					cfg.Screen.Width = width
					cfg.Screen.AvailWidth = width
				}
				if height > 0 {
					cfg.Screen.Height = height
					cfg.Screen.AvailHeight = max(height-30, 0)
				}
			}
		}
	}
	if len(items) > 13 {
		cfg.PerfNow = toFloat(items[13])
	}
	if len(items) > 16 {
		if value := int(toFloat(items[16])); value > 0 {
			cfg.HardwareConcurrency = value
		}
	}
	if len(items) > 17 {
		cfg.TimeOrigin = toFloat(items[17])
	}
	return cfg, nil
}

func (s *turnstileSolver) solve() (string, error) {
	initProgram, err := s.firstProgram()
	if err != nil {
		return "", err
	}
	if _, err := s.disasmProgram(initProgram, true); err != nil {
		return "", err
	}

	secondProgram, err := s.findProgram()
	if err != nil {
		return "", err
	}

	thirdProgramValue, err := s.disasmProgram(secondProgram, true)
	if err != nil {
		return "", err
	}
	thirdProgram, ok := thirdProgramValue.([]any)
	if !ok {
		return "", fmt.Errorf("turnstile nested program has unexpected type %T", thirdProgramValue)
	}

	tokenValue, err := s.disasmProgram(thirdProgram, true)
	if err != nil {
		return "", err
	}
	token, ok := tokenValue.(string)
	if !ok {
		return "", fmt.Errorf("turnstile token has unexpected type %T", tokenValue)
	}
	return token, nil
}

func (s *turnstileSolver) firstProgram() ([]any, error) {
	decoded, err := base64.StdEncoding.DecodeString(s.dx)
	if err != nil {
		return nil, fmt.Errorf("decode turnstile dx: %w", err)
	}

	plain := xorString(string(decoded), s.proof)
	var program []any
	if err := json.Unmarshal([]byte(plain), &program); err != nil {
		return nil, fmt.Errorf("parse turnstile dx: %w", err)
	}
	return program, nil
}

func (s *turnstileSolver) findProgram() ([]any, error) {
	keys := make([]float64, 0, len(s.registers))
	for key := range s.registers {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		program, ok := s.registers[key].([]any)
		if ok && len(program) > 10 {
			return program, nil
		}
	}
	return nil, errors.New("turnstile program register not found")
}

func (s *turnstileSolver) disasmProgram(program []any, countPC bool) (any, error) {
	var result any
	for _, raw := range program {
		if s.ctx != nil {
			if err := s.ctx.Err(); err != nil {
				return nil, err
			}
		}
		instruction, ok := raw.([]any)
		if !ok || len(instruction) == 0 {
			if countPC {
				s.pc++
			}
			continue
		}

		opcode, ok := asFloat64(instruction[0])
		if !ok {
			if countPC {
				s.pc++
			}
			continue
		}
		opName, ok := s.program[opcode]
		if !ok {
			if countPC {
				s.pc++
			}
			continue
		}

		value, err := s.handleOpcode(instruction[1:], opName)
		if err != nil {
			return nil, fmt.Errorf("pc %d %s: %w", s.pc, opName, err)
		}
		result = value
		if countPC {
			s.pc++
		}
	}
	return result, nil
}

func (s *turnstileSolver) handleOpcode(args []any, opName string) (any, error) {
	switch opName {
	case "COPY":
		return s.handleCopy(args)
	case "SET":
		return s.handleSet(args)
	case "GET_INDEX":
		return s.handleGetIndex(args)
	case "BIND":
		return s.handleBind(args)
	case "TRY_CALL_RESULT":
		return s.handleTryCallResult(args)
	case "XOR_REG":
		return s.handleXOR(args)
	case "BTOA":
		return s.handleBTOA(args)
	case "IF_DEFINED":
		return s.handleIfDefined(args)
	case "ATOB":
		return s.handleATOB(args)
	case "JSON_PARSE":
		return s.handleJSONParse(args)
	case "ADD":
		return s.handleAdd(args)
	case "CALL":
		return s.handleCall(args)
	case "JSON_STRINGIFY":
		return s.handleJSONStringify(args)
	case "TRY_CALL":
		return s.handleTryCall(args)
	case "IF_EQUAL":
		return s.handleIfEqual(args)
	case "IF_NOT_CLOSE":
		return s.handleIfNotClose(args)
	case "SUBROUTINE":
		return s.handleSubroutine(args)
	case "AWAIT":
		return s.handleAwait(args)
	case "SUB":
		return s.handleSub(args)
	case "LESS_THAN":
		return s.handleLessThan(args)
	case "MULTIPLY":
		return s.handleMultiply(args)
	case "DIVIDE":
		return s.handleDivide(args)
	case "DEFINE_FUNC", "LOAD_PROGRAM", "REJECT", "SCRIPT_MATCH", "GET_VM", "KEY", "IP":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported opcode %q", opName)
	}
}

func (s *turnstileSolver) handleCopy(args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("copy expects 2 args, got %d", len(args))
	}
	toReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("copy target is not a register")
	}
	fromReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("copy source is not a register")
	}
	if value, ok := s.program[fromReg]; ok {
		s.program[toReg] = value
		return value, nil
	}
	if value, ok := s.registers[fromReg]; ok {
		s.registers[toReg] = value
		return value, nil
	}
	return nil, fmt.Errorf("copy source register %.2f is empty", fromReg)
}

func (s *turnstileSolver) handleSet(args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("set expects 2 args, got %d", len(args))
	}
	reg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("set target is not a register")
	}
	value := args[1]
	if text, ok := value.(string); ok {
		switch text {
		case `window["history"]["length"]`:
			value = 17
		case `window["document"]["location"]`:
			value = turnstileAuthURL
		case `window["navigator"]["storage"]`:
			value = map[string]any{"placement": "replacement"}
		}
	}
	s.registers[reg] = value
	return value, nil
}

func (s *turnstileSolver) handleGetIndex(args []any) (any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("get index expects 3 args, got %d", len(args))
	}
	toReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("get index target is not a register")
	}
	containerReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("get index container is not a register")
	}
	indexReg, ok := asFloat64(args[2])
	if !ok {
		return nil, errors.New("get index key is not a register")
	}

	container, ok := s.valueForRegister(containerReg)
	if !ok {
		return nil, fmt.Errorf("get index container register %.2f is empty", containerReg)
	}
	key, ok := s.valueForRegister(indexReg)
	if !ok {
		return nil, fmt.Errorf("get index key register %.2f is empty", indexReg)
	}

	value := resolveIndex(container, key)
	s.registers[toReg] = value
	return value, nil
}

func (s *turnstileSolver) handleXOR(args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("xor expects 2 args, got %d", len(args))
	}
	reg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("xor register is not a register")
	}
	keyReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("xor key is not a register")
	}
	value, _ := s.valueForRegister(reg)
	key, _ := s.valueForRegister(keyReg)
	result := xorString(symbolString(value), symbolString(key))
	s.registers[reg] = result
	return result, nil
}

func (s *turnstileSolver) handleATOB(args []any) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("atob expects 1 arg, got %d", len(args))
	}
	reg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("atob target is not a register")
	}
	value, _ := s.valueForRegister(reg)
	decoded, err := base64.StdEncoding.DecodeString(symbolString(value))
	if err != nil {
		return nil, err
	}
	result := string(decoded)
	s.registers[reg] = result
	return result, nil
}

func (s *turnstileSolver) handleBTOA(args []any) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("btoa expects 1 arg, got %d", len(args))
	}
	reg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("btoa target is not a register")
	}
	value, _ := s.valueForRegister(reg)
	encoded := base64.StdEncoding.EncodeToString([]byte(symbolString(value)))
	s.registers[reg] = encoded
	return encoded, nil
}

func (s *turnstileSolver) handleBind(args []any) (any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("bind expects 3 args, got %d", len(args))
	}
	toReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("bind target is not a register")
	}
	containerReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("bind container is not a register")
	}
	keyReg, ok := asFloat64(args[2])
	if !ok {
		return nil, errors.New("bind key is not a register")
	}
	container, _ := s.valueForRegister(containerReg)
	key, _ := s.valueForRegister(keyReg)
	result := fmt.Sprintf(`%s[%q]`, symbolString(container), symbolString(key))
	s.registers[toReg] = result
	return result, nil
}

func (s *turnstileSolver) handleTryCallResult(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("try call result expects at least 2 args, got %d", len(args))
	}
	toReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("try call result target is not a register")
	}
	fnReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("try call result function is not a register")
	}
	fnExpr, _ := s.valueForRegister(fnReg)
	callArgs := s.resolveArgs(args[2:])
	value, err := s.invoke(fnExpr, callArgs)
	if err != nil {
		value = err.Error()
	}
	s.registers[toReg] = value
	return value, nil
}

func (s *turnstileSolver) handleTryCall(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("try call expects at least 2 args, got %d", len(args))
	}
	fnReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("try call function is not a register")
	}
	opName, ok := s.program[fnReg]
	if !ok {
		return nil, nil
	}
	return s.handleOpcode(args[2:], opName)
}

func (s *turnstileSolver) handleCall(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("call expects at least 1 arg, got %d", len(args))
	}
	fnReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("call function is not a register")
	}
	fnExpr, _ := s.valueForRegister(fnReg)
	callArgs := s.resolveArgs(args[1:])
	if name, ok := fnExpr.(string); ok && name == "RESOLVE" {
		if len(callArgs) == 0 {
			return "", nil
		}
		return base64.StdEncoding.EncodeToString([]byte(symbolString(callArgs[len(callArgs)-1]))), nil
	}
	if name, ok := fnExpr.(string); ok && strings.Contains(name, "Reflect") && strings.Contains(name, "set") {
		if len(callArgs) >= 3 {
			if obj, ok := callArgs[0].(map[string]any); ok {
				key := symbolString(callArgs[1])
				obj[key] = s.simulateScreenValue(key, callArgs[2])
			}
		}
		return nil, nil
	}
	_, err := s.invoke(fnExpr, callArgs)
	return nil, err
}

func (s *turnstileSolver) handleJSONParse(args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("json parse expects 2 args, got %d", len(args))
	}
	toReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("json parse target is not a register")
	}
	fromReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("json parse source is not a register")
	}
	value, _ := s.valueForRegister(fromReg)
	var parsed any
	if err := json.Unmarshal([]byte(symbolString(value)), &parsed); err != nil {
		return nil, err
	}
	s.registers[toReg] = parsed
	return parsed, nil
}

func (s *turnstileSolver) handleJSONStringify(args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("json stringify expects 2 args, got %d", len(args))
	}
	toReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("json stringify target is not a register")
	}
	fromReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("json stringify source is not a register")
	}
	value, _ := s.valueForRegister(fromReg)

	var result string
	switch typed := value.(type) {
	case string:
		switch typed {
		case `window["navigator"]["hardwareConcurrency"]`:
			result = mustJSONString(s.cfg.HardwareConcurrency)
		case `window["navigator"]["deviceMemory"]`:
			result = mustJSONString(8)
		default:
			result = mustJSONString(typed)
		}
	case []any:
		if containsString(typed, `window["__reactRouterContext"]["state"]["loaderData"]["root"]["clientBootstrap"]["cfConnectingIp"]`) {
			result = `"TypeError: Cannot read properties of undefined (reading 'clientBootstrap')undefinedundefinedundefinedundefinedundefined"`
		} else if containsString(typed, `window["navigator"]["vendor"]`) {
			result = mustJSONString([]any{"Google Inc.", "MacIntel", 8, 0})
		} else {
			result = mustJSONString(typed)
		}
	case map[string]any:
		result = mustJSONString(typed)
	default:
		result = mustJSONString(typed)
	}

	s.registers[toReg] = result
	return result, nil
}

func (s *turnstileSolver) handleAdd(args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("add expects 2 args, got %d", len(args))
	}
	targetReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("add target is not a register")
	}
	sourceReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("add source is not a register")
	}
	current, _ := s.valueForRegister(targetReg)
	value, _ := s.valueForRegister(sourceReg)
	switch typed := current.(type) {
	case []any:
		next := append(typed, value)
		s.registers[targetReg] = next
		return next, nil
	case float64:
		result := typed + toFloat(value)
		s.registers[targetReg] = result
		return result, nil
	default:
		result := symbolString(current) + symbolString(value)
		s.registers[targetReg] = result
		return result, nil
	}
}

func (s *turnstileSolver) handleIfDefined(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("if defined expects at least 2 args, got %d", len(args))
	}
	reg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("if defined value is not a register")
	}
	value, ok := s.valueForRegister(reg)
	if !ok || value == nil {
		return nil, nil
	}
	opcodeReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("if defined opcode is not a register")
	}
	opName, ok := s.program[opcodeReg]
	if !ok {
		return nil, nil
	}
	return s.handleOpcode(args[2:], opName)
}

func (s *turnstileSolver) handleIfEqual(args []any) (any, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("if equal expects at least 3 args, got %d", len(args))
	}
	leftReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("if equal left is not a register")
	}
	rightReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("if equal right is not a register")
	}
	opcodeReg, ok := asFloat64(args[2])
	if !ok {
		return nil, errors.New("if equal opcode is not a register")
	}
	left, _ := s.valueForRegister(leftReg)
	right, _ := s.valueForRegister(rightReg)
	if symbolString(left) != symbolString(right) {
		return nil, nil
	}
	opName, ok := s.program[opcodeReg]
	if !ok {
		return nil, nil
	}
	return s.handleOpcode(args[3:], opName)
}

func (s *turnstileSolver) handleIfNotClose(args []any) (any, error) {
	if len(args) < 4 {
		return nil, fmt.Errorf("if not close expects at least 4 args, got %d", len(args))
	}
	leftReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("if not close left is not a register")
	}
	rightReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("if not close right is not a register")
	}
	thresholdReg, ok := asFloat64(args[2])
	if !ok {
		return nil, errors.New("if not close threshold is not a register")
	}
	opcodeReg, ok := asFloat64(args[3])
	if !ok {
		return nil, errors.New("if not close opcode is not a register")
	}
	left, _ := s.valueForRegister(leftReg)
	right, _ := s.valueForRegister(rightReg)
	threshold, _ := s.valueForRegister(thresholdReg)
	if math.Abs(toFloat(left)-toFloat(right)) <= toFloat(threshold) {
		return nil, nil
	}
	opName, ok := s.program[opcodeReg]
	if !ok {
		return nil, nil
	}
	return s.handleOpcode(args[4:], opName)
}

func (s *turnstileSolver) handleAwait(args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("await expects 2 args, got %d", len(args))
	}
	toReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("await target is not a register")
	}
	fromReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("await source is not a register")
	}
	value, _ := s.valueForRegister(fromReg)
	s.registers[toReg] = value
	return value, nil
}

func (s *turnstileSolver) handleSubroutine(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("subroutine expects at least 2 args, got %d", len(args))
	}
	program, ok := args[1].([]any)
	if !ok {
		return nil, errors.New("subroutine payload is not a program")
	}
	return s.disasmProgram(program, false)
}

func (s *turnstileSolver) handleSub(args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("sub expects 2 args, got %d", len(args))
	}
	targetReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("sub target is not a register")
	}
	sourceReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("sub source is not a register")
	}
	current, _ := s.valueForRegister(targetReg)
	value, _ := s.valueForRegister(sourceReg)
	result := toFloat(current) - toFloat(value)
	s.registers[targetReg] = result
	return result, nil
}

func (s *turnstileSolver) handleLessThan(args []any) (any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("less than expects 3 args, got %d", len(args))
	}
	toReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("less than target is not a register")
	}
	leftReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("less than left is not a register")
	}
	rightReg, ok := asFloat64(args[2])
	if !ok {
		return nil, errors.New("less than right is not a register")
	}
	left, _ := s.valueForRegister(leftReg)
	right, _ := s.valueForRegister(rightReg)
	result := toFloat(left) < toFloat(right)
	s.registers[toReg] = result
	return result, nil
}

func (s *turnstileSolver) handleMultiply(args []any) (any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("multiply expects 3 args, got %d", len(args))
	}
	toReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("multiply target is not a register")
	}
	leftReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("multiply left is not a register")
	}
	rightReg, ok := asFloat64(args[2])
	if !ok {
		return nil, errors.New("multiply right is not a register")
	}
	left, _ := s.valueForRegister(leftReg)
	right, _ := s.valueForRegister(rightReg)
	result := toFloat(left) * toFloat(right)
	s.registers[toReg] = result
	return result, nil
}

func (s *turnstileSolver) handleDivide(args []any) (any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("divide expects 3 args, got %d", len(args))
	}
	toReg, ok := asFloat64(args[0])
	if !ok {
		return nil, errors.New("divide target is not a register")
	}
	leftReg, ok := asFloat64(args[1])
	if !ok {
		return nil, errors.New("divide left is not a register")
	}
	rightReg, ok := asFloat64(args[2])
	if !ok {
		return nil, errors.New("divide right is not a register")
	}
	left, _ := s.valueForRegister(leftReg)
	right, _ := s.valueForRegister(rightReg)
	divisor := toFloat(right)
	if divisor == 0 {
		s.registers[toReg] = float64(0)
		return float64(0), nil
	}
	result := toFloat(left) / divisor
	s.registers[toReg] = result
	return result, nil
}

func (s *turnstileSolver) invoke(fnExpr any, args []any) (any, error) {
	name := symbolString(fnExpr)
	switch name {
	case `window["performance"]["now"]`:
		return s.cfg.PerfNow, nil
	case `window["Object"]["create"]`:
		return map[string]any{}, nil
	case `window["document"]["createElement"]`:
		if len(args) == 0 {
			return nil, errors.New("createElement missing tag name")
		}
		switch symbolString(args[0]) {
		case "div":
			return "<div></div>", nil
		case "canvas":
			return "<canvas>", nil
		case "iframe":
			return "<iframe>", nil
		default:
			return "<element>", nil
		}
	case `window["Math"]["random"]`:
		return s.rng.Float64(), nil
	case `window["Date"]["now"]`:
		return math.Round(s.cfg.TimeOrigin + s.cfg.PerfNow), nil
	case `<canvas>["getContext"]`:
		if len(args) == 0 {
			return nil, nil
		}
		kind := symbolString(args[0])
		if kind == "webgl" || kind == "experimental-webgl" {
			return "<webgl_context>", nil
		}
		return nil, nil
	case `window["document"]["body"]["appendChild"]`, `window["document"]["body"]["removeChild"]`:
		if len(args) == 0 {
			return nil, nil
		}
		return args[0], nil
	case `<div></div>["getBoundingClientRect"]`:
		return map[string]any{
			"x":      0,
			"y":      744.6875,
			"width":  19.640625,
			"height": 14,
			"top":    744.6875,
			"right":  19.640625,
			"bottom": 758.6875,
			"left":   0,
		}, nil
	case `window["Object"]["keys"]`:
		if len(args) == 1 && symbolString(args[0]) == `window["localStorage"]` {
			return strings.Join(s.cfg.LocalStorageKeys, ","), nil
		}
		return nil, fmt.Errorf("unsupported Object.keys target %v", args)
	case `window["localStorage"]["setItem"]`:
		if len(args) >= 2 {
			s.localStorage[symbolString(args[0])] = symbolString(args[1])
		}
		return nil, nil
	case `{'placement': 'replacement'}["estimate"]`, `window["navigator"]["storage"]["estimate"]`:
		return map[string]any{
			"quota": 296630877388,
			"usage": 913230,
			"usageDetails": map[string]any{
				"indexedDB": 913230,
			},
		}, nil
	case `<webgl_context>["getExtension"]`:
		if len(args) == 1 && symbolString(args[0]) == "WEBGL_debug_renderer_info" {
			return "<webgl_debug_ext>", nil
		}
		return nil, nil
	case `<webgl_context>["getParameter"]`:
		if len(args) == 0 {
			return nil, nil
		}
		switch symbolString(args[0]) {
		case "UNMASKED_VENDOR_WEBGL", "37445":
			return "Intel", nil
		case "UNMASKED_RENDERER_WEBGL", "37446":
			return "Mesa Intel(R) UHD Graphics 630 (CFL GT2)", nil
		default:
			return nil, nil
		}
	}

	if strings.Contains(name, "getTimezoneOffset") {
		return -60, nil
	}
	if strings.Contains(name, "toLocaleString") {
		return "24/02/2026, 17:34:21", nil
	}
	return nil, fmt.Errorf("unsupported call %s", name)
}

func (s *turnstileSolver) simulateScreenValue(key string, fallback any) any {
	screen := map[string]any{
		"availWidth":  s.cfg.Screen.AvailWidth,
		"availHeight": s.cfg.Screen.AvailHeight,
		"availLeft":   s.cfg.Screen.AvailLeft,
		"availTop":    s.cfg.Screen.AvailTop,
		"colorDepth":  s.cfg.Screen.ColorDepth,
		"height":      s.cfg.Screen.Height,
		"width":       s.cfg.Screen.Width,
		"pixelDepth":  s.cfg.Screen.PixelDepth,
	}
	if value, ok := screen[key]; ok {
		return value
	}
	return fallback
}

func (s *turnstileSolver) valueForRegister(reg float64) (any, bool) {
	if value, ok := s.program[reg]; ok {
		return value, true
	}
	value, ok := s.registers[reg]
	return value, ok
}

func (s *turnstileSolver) resolveArgs(args []any) []any {
	resolved := make([]any, 0, len(args))
	for _, arg := range args {
		reg, ok := asFloat64(arg)
		if !ok {
			resolved = append(resolved, arg)
			continue
		}
		value, _ := s.valueForRegister(reg)
		resolved = append(resolved, value)
	}
	return resolved
}

func resolveIndex(container, key any) any {
	keyText := symbolString(key)
	switch typed := container.(type) {
	case map[string]any:
		if value, ok := typed[keyText]; ok {
			return value
		}
	case []any:
		if index, ok := asInt(key); ok && index >= 0 && index < len(typed) {
			return typed[index]
		}
	}
	return fmt.Sprintf(`%s[%q]`, symbolString(container), keyText)
}

func xorString(value, key string) string {
	if key == "" {
		return value
	}
	valueBytes := []byte(value)
	keyBytes := []byte(key)
	out := make([]byte, len(valueBytes))
	for i, b := range valueBytes {
		out[i] = b ^ keyBytes[i%len(keyBytes)]
	}
	return string(out)
}

func symbolString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case string:
		return typed
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case float64:
		if math.Trunc(typed) == typed {
			return fmt.Sprintf("%.0f", typed)
		}
		return fmt.Sprint(typed)
	case int:
		return fmt.Sprint(typed)
	case map[string]any:
		if len(typed) == 1 && typed["placement"] == "replacement" {
			return "{'placement': 'replacement'}"
		}
		return mustJSONString(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func mustJSONString(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(body)
}

func containsString(values []any, want string) bool {
	for _, value := range values {
		if symbolString(value) == want {
			return true
		}
	}
	return false
}

func asFloat64(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	default:
		return 0, false
	}
}

func asInt(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func toFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}
