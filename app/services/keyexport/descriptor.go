package keyexport

import (
	"fmt"
	"strings"
)

// BIP-380 output descriptor checksum, which Bitcoin Core's importdescriptors requires.
const (
	descriptorInputCharset    = "0123456789()[],'/*abcdefgh@:$%{}IJKLMNOPQRSTUVWXYZ&+-.;<=>?!^_|~ijklmnopqrstuvwxyzABCDEFGH`#\"\\ "
	descriptorChecksumCharset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	descriptorChecksumLength  = 8
	descriptorGroupSize       = 3
)

var descriptorGenerator = [5]uint64{0xf5dee51989, 0xa9fdca3312, 0x1bab10e32d, 0x3706b1677a, 0x644d626ffd}

// DescriptorChecksum returns the 8-character BIP-380 checksum of descriptor.
func DescriptorChecksum(descriptor string) (string, error) {
	symbols, err := expandDescriptor(descriptor)
	if err != nil {
		return "", err
	}
	symbols = append(symbols, make([]uint64, descriptorChecksumLength)...)
	checksum := descriptorPolymod(symbols) ^ 1
	var out strings.Builder
	for i := 0; i < descriptorChecksumLength; i++ {
		out.WriteByte(descriptorChecksumCharset[(checksum>>(5*(descriptorChecksumLength-1-i)))&31])
	}
	return out.String(), nil
}

func expandDescriptor(descriptor string) ([]uint64, error) {
	symbols := make([]uint64, 0, len(descriptor)+len(descriptor)/descriptorGroupSize+1)
	groups := make([]uint64, 0, descriptorGroupSize)
	for _, c := range descriptor {
		position := strings.IndexRune(descriptorInputCharset, c)
		if position < 0 {
			return nil, fmt.Errorf("descriptor holds a character outside the BIP-380 charset")
		}
		value := uint64(position)
		symbols = append(symbols, value&31)
		groups = append(groups, value>>5)
		if len(groups) == descriptorGroupSize {
			symbols = append(symbols, groups[0]*9+groups[1]*3+groups[2])
			groups = groups[:0]
		}
	}
	switch len(groups) {
	case 1:
		symbols = append(symbols, groups[0])
	case 2:
		symbols = append(symbols, groups[0]*3+groups[1])
	}
	return symbols, nil
}

func descriptorPolymod(symbols []uint64) uint64 {
	checksum := uint64(1)
	for _, value := range symbols {
		top := checksum >> 35
		checksum = (checksum&0x7ffffffff)<<5 ^ value
		for i, generator := range descriptorGenerator {
			if (top>>uint(i))&1 == 1 {
				checksum ^= generator
			}
		}
	}
	return checksum
}
