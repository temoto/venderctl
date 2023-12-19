// Code generated by command: go run keyset_asm.go -pkg keyset -out ../keyset/keyset_amd64.s -stubs ../keyset/keyset_amd64.go. DO NOT EDIT.

//go:build !purego

#include "textflag.h"

// func Lookup(keyset []byte, key []byte) int
// Requires: AVX
TEXT ·Lookup(SB), NOSPLIT, $0-56
	MOVQ keyset_base+0(FP), AX
	MOVQ keyset_len+8(FP), CX
	SHRQ $0x04, CX
	MOVQ key_base+24(FP), DX
	MOVQ key_len+32(FP), BX
	MOVQ key_cap+40(FP), SI
	CMPQ BX, $0x10
	JA   not_found
	CMPQ SI, $0x10
	JB   safe_load

load:
	VMOVUPS (DX), X0

prepare:
	VPXOR     X2, X2, X2
	VPCMPEQB  X1, X1, X1
	LEAQ      blend_masks<>+16(SB), DX
	SUBQ      BX, DX
	VMOVUPS   (DX), X3
	VPBLENDVB X3, X0, X2, X0
	XORQ      DX, DX
	MOVQ      CX, BX
	SHRQ      $0x02, BX
	SHLQ      $0x02, BX

bigloop:
	CMPQ     DX, BX
	JE       loop
	VPCMPEQB (AX), X0, X8
	VPTEST   X1, X8
	JCS      done
	VPCMPEQB 16(AX), X0, X9
	VPTEST   X1, X9
	JCS      found1
	VPCMPEQB 32(AX), X0, X10
	VPTEST   X1, X10
	JCS      found2
	VPCMPEQB 48(AX), X0, X11
	VPTEST   X1, X11
	JCS      found3
	ADDQ     $0x04, DX
	ADDQ     $0x40, AX
	JMP      bigloop

loop:
	CMPQ     DX, CX
	JE       done
	VPCMPEQB (AX), X0, X2
	VPTEST   X1, X2
	JCS      done
	INCQ     DX
	ADDQ     $0x10, AX
	JMP      loop
	JMP done

found3:
	INCQ DX

found2:
	INCQ DX

found1:
	INCQ DX

done:
	MOVQ DX, ret+48(FP)
	RET

not_found:
	MOVQ CX, ret+48(FP)
	RET

safe_load:
	MOVQ    DX, SI
	ANDQ    $0x00000fff, SI
	CMPQ    SI, $0x00000ff0
	JBE     load
	MOVQ    $0xfffffffffffffff0, SI
	ADDQ    BX, SI
	VMOVUPS (DX)(SI*1), X0
	LEAQ    shuffle_masks<>+16(SB), DX
	SUBQ    BX, DX
	VMOVUPS (DX), X1
	VPSHUFB X1, X0, X0
	JMP     prepare

DATA blend_masks<>+0(SB)/8, $0xffffffffffffffff
DATA blend_masks<>+8(SB)/8, $0xffffffffffffffff
DATA blend_masks<>+16(SB)/8, $0x0000000000000000
DATA blend_masks<>+24(SB)/8, $0x0000000000000000
GLOBL blend_masks<>(SB), RODATA|NOPTR, $32

DATA shuffle_masks<>+0(SB)/8, $0x0706050403020100
DATA shuffle_masks<>+8(SB)/8, $0x0f0e0d0c0b0a0908
DATA shuffle_masks<>+16(SB)/8, $0x0706050403020100
DATA shuffle_masks<>+24(SB)/8, $0x0f0e0d0c0b0a0908
GLOBL shuffle_masks<>(SB), RODATA|NOPTR, $32
