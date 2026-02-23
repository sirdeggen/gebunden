#!/usr/bin/env node
import { WalletClient, IdentityClient } from '@bsv/sdk';
import { PeerPayClient } from '@bsv/message-box-client';
// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------
const MESSAGE_BOX_URL = process.env.MESSAGE_BOX_URL ?? 'https://messagebox.babbage.systems';
// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------
const wallet = new WalletClient('auto', 'pay');
const identityClient = new IdentityClient(wallet);
const peerPay = new PeerPayClient({
    walletClient: wallet,
    messageBoxHost: MESSAGE_BOX_URL,
    enableLogging: false,
});
// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
function isHexPublicKey(str) {
    return /^0[23][0-9a-fA-F]{64}$/.test(str);
}
async function resolveRecipient(target) {
    if (isHexPublicKey(target)) {
        return target;
    }
    process.stderr.write(`Resolving ${target} ...\n`);
    const results = await identityClient.resolveByAttributes({ attributes: { any: target } });
    if (!results || results.length === 0) {
        throw new Error(`No identity found for "${target}"`);
    }
    const key = results[0].identityKey;
    process.stderr.write(`Found identity key: ${key}\n`);
    return key;
}
function usage() {
    console.error('Usage:');
    console.error('  pay send <recipient> <satoshis>   Send a BRC-29 payment');
    console.error('  pay receive                       List and accept inbound payments');
    console.error('  pay identity                      Show your identity public key');
    console.error('  pay history                       Show recent payment history');
    console.error('');
    console.error('recipient can be a 66-char hex identity key, or a name/email/paymail');
}
// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------
async function cmdSend(target, amountStr) {
    const amount = Number.parseInt(amountStr, 10);
    if (Number.isNaN(amount) || amount <= 0) {
        console.error('Error: satoshis must be a positive integer.');
        process.exit(1);
    }
    const recipient = await resolveRecipient(target);
    process.stderr.write(`Sending ${amount.toLocaleString()} sats to ${recipient.slice(0, 16)}...\n`);
    await peerPay.sendPayment({ recipient, amount });
    console.log('Payment sent successfully!');
}
async function cmdReceive() {
    process.stderr.write('Checking for inbound payments ...\n');
    const payments = await peerPay.listIncomingPayments();
    if (!payments || payments.length === 0) {
        console.log('No pending payments.');
        return;
    }
    payments.forEach((p, i) => {
        const sats = p.token?.amount ?? '?';
        const sender = p.sender ? p.sender.slice(0, 14) + '...' : 'unknown';
        console.log(`  [${i + 1}] ${Number(sats).toLocaleString()} sats from ${sender}`);
    });
    let accepted = 0;
    for (let i = 0; i < payments.length; i++) {
        const p = payments[i];
        process.stderr.write(`Accepting payment ${i + 1} ... `);
        try {
            await peerPay.acceptPayment(p);
            process.stderr.write('done.\n');
            accepted++;
        }
        catch {
            // Retry once with a fresh listing in case the token is stale
            try {
                const fresh = await peerPay.listIncomingPayments();
                const match = fresh.find((x) => String(x.messageId) === String(p.messageId));
                if (!match)
                    throw new Error('Payment not found on refresh');
                await peerPay.acceptPayment(match);
                process.stderr.write('done.\n');
                accepted++;
            }
            catch (error_) {
                process.stderr.write(`failed: ${error_.message}\n`);
            }
        }
    }
    console.log(`${accepted} payment${accepted === 1 ? '' : 's'} received.`);
}
async function cmdIdentity() {
    const result = await wallet.getPublicKey({ identityKey: true });
    console.log(result.publicKey);
}
async function cmdHistory() {
    const response = await wallet.listActions({
        labels: ['peerpay'],
        labelQueryMode: 'any',
        includeOutputs: true,
        includeOutputLockingScripts: true,
        limit: 20,
    });
    const actions = response?.actions ?? [];
    if (actions.length === 0) {
        console.log('No payment history found.');
        return;
    }
    for (const action of actions) {
        const sats = action.satoshis ?? 0;
        const dir = sats < 0 ? 'sent' : 'received';
        const abs = Math.abs(sats);
        console.log(`${dir.padEnd(8)} ${abs.toLocaleString().padStart(12)} sats  txid: ${action.txid?.slice(0, 16)}...`);
    }
}
// ---------------------------------------------------------------------------
// Entrypoint
// ---------------------------------------------------------------------------
const [, , subcmd, ...args] = process.argv;
try {
    switch (subcmd) {
        case 'send': {
            const [target, amountStr] = args;
            if (!target || !amountStr) {
                console.error('Usage: pay send <recipient> <satoshis>');
                process.exit(1);
            }
            await cmdSend(target, amountStr);
            break;
        }
        case 'receive':
            await cmdReceive();
            break;
        case 'identity':
            await cmdIdentity();
            break;
        case 'history':
            await cmdHistory();
            break;
        default:
            usage();
            process.exit(subcmd ? 1 : 0);
    }
}
catch (err) {
    console.error(`Error: ${err.message}`);
    process.exit(1);
}
//# sourceMappingURL=index.js.map