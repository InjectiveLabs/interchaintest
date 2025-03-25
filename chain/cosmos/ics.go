package cosmos

import (
	"context"
	"fmt"

	// nolint:staticcheck

	stakingttypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/strangelove-ventures/interchaintest/v8/ibc"
	"github.com/strangelove-ventures/interchaintest/v8/testreporter"
)

const (
	icsVer330 = "v3.3.0"
	icsVer400 = "v4.0.0"
)

// FinishICSProviderSetup sets up the base of an ICS connection with respect to the relayer, provider actions, and flushing of packets.
// 1. Stop the relayer, then start it back up. This completes the ICS20-1 transfer channel setup.
//   - You must set look-back block history >100 blocks in [interchaintest.NewBuiltinRelayerFactory].
//
// 2. Get the first provider validator, and delegate 1,000,000denom to it. This triggers a CometBFT power increase of 1.
// 3. Flush the pending ICS packets to the consumer chain.
func (c *CosmosChain) FinishICSProviderSetup(ctx context.Context, r ibc.Relayer, eRep *testreporter.RelayerExecReporter, ibcPath string) error {
	// Restart the relayer to finish IBC transfer connection w/ ics20-1 link
	if err := r.StopRelayer(ctx, eRep); err != nil {
		return fmt.Errorf("failed to stop relayer: %w", err)
	}
	if err := r.StartRelayer(ctx, eRep); err != nil {
		return fmt.Errorf("failed to start relayer: %w", err)
	}

	// perform provider delegation to complete provider<>consumer channel connection
	stakingVals, err := c.StakingQueryValidators(ctx, stakingttypes.BondStatusBonded)
	if err != nil {
		return fmt.Errorf("failed to query validators: %w", err)
	}

	providerVal := stakingVals[0]

	err = c.GetNode().StakingDelegate(ctx, "validator", providerVal.OperatorAddress, fmt.Sprintf("1000000%s", c.Config().Denom))
	if err != nil {
		return fmt.Errorf("failed to delegate to validator: %w", err)
	}

	stakingVals, err = c.StakingQueryValidators(ctx, stakingttypes.BondStatusBonded)
	if err != nil {
		return fmt.Errorf("failed to query validators: %w", err)
	}
	var providerAfter *stakingttypes.Validator
	for _, val := range stakingVals {
		if val.OperatorAddress == providerVal.OperatorAddress {
			providerAfter = &val
			break
		}
	}
	if providerAfter == nil {
		return fmt.Errorf("failed to find provider validator after delegation")
	}
	if providerAfter.Tokens.LT(providerVal.Tokens) {
		return fmt.Errorf("delegation failed; before: %v, after: %v", providerVal.Tokens, providerAfter.Tokens)
	}

	return c.FlushPendingICSPackets(ctx, r, eRep, ibcPath)
}

// FlushPendingICSPackets flushes the pending ICS packets to the consumer chain from the "provider" port.
func (c *CosmosChain) FlushPendingICSPackets(ctx context.Context, r ibc.Relayer, eRep *testreporter.RelayerExecReporter, ibcPath string) error {
	channels, err := r.GetChannels(ctx, eRep, c.cfg.ChainID)
	if err != nil {
		return fmt.Errorf("failed to get channels: %w", err)
	}

	ICSChannel := ""
	for _, channel := range channels {
		if channel.PortID == "provider" {
			ICSChannel = channel.ChannelID
		}
	}

	return r.Flush(ctx, eRep, ibcPath, ICSChannel)
}
