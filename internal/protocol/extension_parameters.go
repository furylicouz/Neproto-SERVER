package protocol

const (
	minUDPAssociations       = 1
	maxUDPAssociations       = 4096
	minUDPPayload            = 1200
	minUnreliablePayload     = 512
	maxUDPPayload            = 65507
	minUDPIdleTimeoutMS      = 5000
	maxUDPIdleTimeoutMS      = 600000
	minSessionReceiveBytes   = 1024 * 1024
	maxSessionReceiveBytes   = 256 * 1024 * 1024
	minExtensionStreamWindow = 64 * 1024
	maxExtensionStreamWindow = 16 * 1024 * 1024
)

type ExtensionParameters struct {
	Capabilities           ExtensionCapability
	MaxUDPAssociations     uint64
	MaxUDPPayload          uint64
	UDPIdleTimeoutMS       uint64
	MaxSessionReceiveBytes uint64
	MaxStreamWindowBytes   uint64
	UnreliableDatagramSize uint64
	ForwardSecretKeyShare  [32]byte
}

func (p ExtensionParameters) Envelope(
	messageType ExtensionMessageType,
	messageID uint64,
) (ExtensionEnvelope, error) {
	if err := p.validate(); err != nil {
		return ExtensionEnvelope{}, err
	}
	parameters := make([]ExtensionTLV, 0, 8)
	parameters = append(parameters, NewExtensionCapabilitiesTLV(p.Capabilities))
	values := []struct {
		tlvType uint64
		value   uint64
	}{
		{ExtensionTLVMaxUDPAssociations, p.MaxUDPAssociations},
		{ExtensionTLVMaxUDPPayload, p.MaxUDPPayload},
		{ExtensionTLVUDPIdleTimeoutMS, p.UDPIdleTimeoutMS},
		{ExtensionTLVMaxSessionReceiveBytes, p.MaxSessionReceiveBytes},
		{ExtensionTLVMaxStreamWindowBytes, p.MaxStreamWindowBytes},
		{ExtensionTLVUnreliableDatagramSize, p.UnreliableDatagramSize},
	}
	for _, value := range values {
		tlv, err := NewExtensionUvarintTLV(value.tlvType, value.value)
		if err != nil {
			return ExtensionEnvelope{}, err
		}
		parameters = append(parameters, tlv)
	}
	if p.Capabilities&CapabilityForwardSecrecy != 0 {
		parameters = append(parameters, ExtensionTLV{
			Type:  ExtensionTLVForwardSecretKeyShare,
			Value: append([]byte(nil), p.ForwardSecretKeyShare[:]...),
		})
	}
	envelope := ExtensionEnvelope{Type: messageType, MessageID: messageID, TLVs: parameters}
	if _, err := envelope.MarshalBinary(); err != nil {
		return ExtensionEnvelope{}, err
	}
	return envelope, nil
}

func ParseExtensionParameters(envelope ExtensionEnvelope) (ExtensionParameters, error) {
	if _, err := envelope.MarshalBinary(); err != nil {
		return ExtensionParameters{}, err
	}
	var parameters ExtensionParameters
	seen := make(map[uint64]struct{}, 7)
	for _, tlv := range envelope.TLVs {
		baseType := tlv.BaseType()
		if _, duplicate := seen[baseType]; duplicate {
			return ExtensionParameters{}, ErrInvalidExtension
		}
		seen[baseType] = struct{}{}
		switch baseType {
		case ExtensionTLVCapabilities:
			capabilities, err := tlv.Capabilities()
			if err != nil {
				return ExtensionParameters{}, err
			}
			parameters.Capabilities = capabilities
		case ExtensionTLVMaxUDPAssociations:
			value, err := tlv.Uvarint()
			if err != nil {
				return ExtensionParameters{}, err
			}
			parameters.MaxUDPAssociations = value
		case ExtensionTLVMaxUDPPayload:
			value, err := tlv.Uvarint()
			if err != nil {
				return ExtensionParameters{}, err
			}
			parameters.MaxUDPPayload = value
		case ExtensionTLVUDPIdleTimeoutMS:
			value, err := tlv.Uvarint()
			if err != nil {
				return ExtensionParameters{}, err
			}
			parameters.UDPIdleTimeoutMS = value
		case ExtensionTLVMaxSessionReceiveBytes:
			value, err := tlv.Uvarint()
			if err != nil {
				return ExtensionParameters{}, err
			}
			parameters.MaxSessionReceiveBytes = value
		case ExtensionTLVMaxStreamWindowBytes:
			value, err := tlv.Uvarint()
			if err != nil {
				return ExtensionParameters{}, err
			}
			parameters.MaxStreamWindowBytes = value
		case ExtensionTLVUnreliableDatagramSize:
			value, err := tlv.Uvarint()
			if err != nil {
				return ExtensionParameters{}, err
			}
			parameters.UnreliableDatagramSize = value
		case ExtensionTLVForwardSecretKeyShare:
			if len(tlv.Value) != len(parameters.ForwardSecretKeyShare) {
				return ExtensionParameters{}, ErrInvalidExtension
			}
			copy(parameters.ForwardSecretKeyShare[:], tlv.Value)
		default:
			if tlv.Mandatory() {
				return ExtensionParameters{}, ErrUnsupportedExtension
			}
		}
	}
	for required := ExtensionTLVCapabilities; required <= ExtensionTLVUnreliableDatagramSize; required++ {
		if _, exists := seen[required]; !exists {
			return ExtensionParameters{}, ErrInvalidExtension
		}
	}
	if err := parameters.validate(); err != nil {
		return ExtensionParameters{}, err
	}
	return parameters, nil
}

func ValidateExtensionAccept(offer, accept ExtensionParameters) error {
	if err := offer.validate(); err != nil {
		return err
	}
	if err := accept.validate(); err != nil {
		return err
	}
	if accept.Capabilities&^offer.Capabilities != 0 ||
		accept.MaxUDPAssociations > offer.MaxUDPAssociations ||
		accept.MaxUDPPayload > offer.MaxUDPPayload ||
		accept.UDPIdleTimeoutMS > offer.UDPIdleTimeoutMS ||
		accept.MaxSessionReceiveBytes > offer.MaxSessionReceiveBytes ||
		accept.MaxStreamWindowBytes > offer.MaxStreamWindowBytes ||
		accept.UnreliableDatagramSize > offer.UnreliableDatagramSize {
		return ErrUnsupportedExtension
	}
	return nil
}

func (p ExtensionParameters) validate() error {
	if p.MaxSessionReceiveBytes < minSessionReceiveBytes ||
		p.MaxSessionReceiveBytes > maxSessionReceiveBytes ||
		p.MaxStreamWindowBytes < minExtensionStreamWindow ||
		p.MaxStreamWindowBytes > maxExtensionStreamWindow ||
		p.MaxStreamWindowBytes > p.MaxSessionReceiveBytes {
		return ErrInvalidExtension
	}
	if p.Capabilities&CapabilityReliableUDP == 0 {
		if p.MaxUDPAssociations != 0 || p.MaxUDPPayload != 0 || p.UDPIdleTimeoutMS != 0 ||
			p.UnreliableDatagramSize != 0 || p.Capabilities&CapabilityUnreliableDatagrams != 0 {
			return ErrInvalidExtension
		}
		return validateForwardSecretParameters(p)
	}
	if p.MaxUDPAssociations < minUDPAssociations || p.MaxUDPAssociations > maxUDPAssociations ||
		p.MaxUDPPayload < minUDPPayload || p.MaxUDPPayload > maxUDPPayload ||
		p.UDPIdleTimeoutMS < minUDPIdleTimeoutMS || p.UDPIdleTimeoutMS > maxUDPIdleTimeoutMS {
		return ErrInvalidExtension
	}
	if p.Capabilities&CapabilityUnreliableDatagrams == 0 {
		if p.UnreliableDatagramSize != 0 {
			return ErrInvalidExtension
		}
		return validateForwardSecretParameters(p)
	}
	if p.UnreliableDatagramSize < minUnreliablePayload || p.UnreliableDatagramSize > p.MaxUDPPayload {
		return ErrInvalidExtension
	}
	return validateForwardSecretParameters(p)
}

func validateForwardSecretParameters(p ExtensionParameters) error {
	zero := [32]byte{}
	if p.Capabilities&CapabilityForwardSecrecy != 0 {
		if p.ForwardSecretKeyShare == zero {
			return ErrInvalidExtension
		}
		return nil
	}
	if p.ForwardSecretKeyShare != zero {
		return ErrInvalidExtension
	}
	return nil
}
