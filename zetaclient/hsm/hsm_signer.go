package hsm

import (
	"os"

	"github.com/cosmos/cosmos-sdk/client"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	"github.com/frumioj/crypto11"
	"github.com/pkg/errors"
	keystone "github.com/zeta-chain/keystone/keys"
)

const hsmPath = "HSM_PATH"
const hsmPIN = "HSM_PIN"
const hsmLabel = "HSM_LABEL"

// Sign Generates signature of msg using the key indexed by the label through the HSM defined in the config
func Sign(config *crypto11.Config, msg []byte, label string) (signature []byte, err error) {
	keyring, err := keystone.NewPkcs11(config)
	if err != nil {
		return
	}
	key, err := keyring.Key(label)
	if err != nil {
		return
	}
	return key.Sign(msg, nil)
}

// GenerateKey This generates a new key using one of the supported algorithms and a label identifier through the HSM
func GenerateKey(label string, algorithm keystone.KeygenAlgorithm, config *crypto11.Config) (*keystone.CryptoKey, error) {
	keyring, err := keystone.NewPkcs11(config)
	if err != nil {
		return nil, err
	}
	return keyring.NewKey(algorithm, label)
}

// GetHSMAddress This address is generated by secp256k1 curve from cosmos sdk
func GetHSMAddress(config *crypto11.Config, label string) (types.Address, types.PubKey, error) {
	keyring, err := keystone.NewPkcs11(config)
	if err != nil {
		return nil, nil, err
	}
	key, err := keyring.Key(label)
	if err != nil {
		return nil, nil, err
	}
	pubKey := key.PubKey()
	return pubKey.Address(), pubKey, nil
}

// SignWithHSM signs a given tx with a named key.
// This is adapted from github.com/cosmos/cosmos-sdk/client/tx Sign() function; Modified to use an HSM.
// The resulting signature will be added to the transaction builder overwriting the previous
// ones if overwrite=true (otherwise, the signature will be appended).
// Signing a transaction with multiple signers in the DIRECT mode is not supported and will
// return an error.
// An error is returned upon failure.
func SignWithHSM(
	txf clienttx.Factory,
	name string,
	txBuilder client.TxBuilder,
	overwriteSig bool,
	txConfig client.TxConfig,
) error {
	hsmCfg, err := GetPKCS11Config()
	if err != nil {
		return err
	}

	address, pubKey, err := GetHSMAddress(hsmCfg, name)
	if err != nil {
		return err
	}

	signerData := authsigning.SignerData{
		ChainID:       txf.ChainID(),
		AccountNumber: txf.AccountNumber(),
		Sequence:      txf.Sequence(),
		PubKey:        pubKey,
		Address:       sdk.AccAddress(address).String(),
	}

	signMode := txf.SignMode()

	// For SIGN_MODE_DIRECT, calling SetSignatures calls setSignerInfos on
	// TxBuilder under the hood, and SignerInfos is needed to generate the
	// sign bytes. This is the reason for setting SetSignatures here, with a
	// nil signature.
	//
	// Note: this line is not needed for SIGN_MODE_LEGACY_AMINO, but putting it
	// also doesn't affect its generated sign bytes, so for code's simplicity
	// sake, we put it here.
	sigData := signing.SingleSignatureData{
		SignMode:  signMode,
		Signature: nil,
	}
	sig := signing.SignatureV2{
		PubKey:   pubKey,
		Data:     &sigData,
		Sequence: txf.Sequence(),
	}

	var prevSignatures []signing.SignatureV2
	if !overwriteSig {
		prevSignatures, err = txBuilder.GetTx().GetSignaturesV2()
		if err != nil {
			return err
		}
	}
	// Overwrite or append signer infos.
	var sigs []signing.SignatureV2
	if overwriteSig {
		sigs = []signing.SignatureV2{sig}
	} else {
		sigs = append(prevSignatures, sig) //nolint:gocritic
	}
	if err := txBuilder.SetSignatures(sigs...); err != nil {
		return err
	}

	// Generate the bytes to be signed.
	bytesToSign, err := txConfig.SignModeHandler().GetSignBytes(signMode, signerData, txBuilder.GetTx())
	if err != nil {
		return err
	}

	// Sign those bytes
	sigBytes, err := Sign(hsmCfg, bytesToSign, name)
	if err != nil {
		return err
	}

	// Construct the SignatureV2 struct
	sigData = signing.SingleSignatureData{
		SignMode:  signMode,
		Signature: sigBytes,
	}
	sig = signing.SignatureV2{
		PubKey:   pubKey,
		Data:     &sigData,
		Sequence: txf.Sequence(),
	}

	if overwriteSig {
		return txBuilder.SetSignatures(sig)
	}
	prevSignatures = append(prevSignatures, sig)
	return txBuilder.SetSignatures(prevSignatures...)
}

func GetPKCS11Config() (config *crypto11.Config, err error) {
	config = &crypto11.Config{}
	config.Path = os.Getenv(hsmPath)
	config.Pin = os.Getenv(hsmPIN)
	config.TokenLabel = os.Getenv(hsmLabel)

	if config.Path == "" || config.Pin == "" || config.TokenLabel == "" {
		err = errors.New("error getting pkcs11 config, make sure env variables are set")
	}
	return
}