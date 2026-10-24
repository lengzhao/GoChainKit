#!/usr/bin/env python3
"""Real Docker acceptance; preserves volumes and restores network on failure.

Run only against a development network: creates wallets/transfers, restarts one
node, and temporarily partitions node3. Uses only the Python standard library.
"""
import argparse
import base64
import json
import os
from pathlib import Path
import struct
import subprocess
import time
import urllib.error
import urllib.request


ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", default="gochainkit")
    parser.add_argument("--base-port", type=int, default=8081)
    parser.add_argument("--skip-start", action="store_true")
    parser.add_argument("--report", type=Path)
    args = parser.parse_args()
    if not 1 <= args.base_port <= 65533:
        parser.error("base port must be 1..65533")
    env = dict(os.environ)
    for i in range(3):
        env[f"GOCHAINKIT_PORT{i+1}"] = str(args.base_port + i)
    compose = ["docker", "compose", "-p", args.project]
    results = []
    detached = None

    def run(command, timeout=90, input_text=None):
        result = subprocess.run(command, cwd=ROOT, env=env, capture_output=True,
                                text=True, timeout=timeout, input=input_text)
        if result.returncode:
            raise RuntimeError(f"{' '.join(command)}: {result.stderr[-4000:]}")
        return result.stdout

    def api(index, path, data=None):
        body = None if data is None else json.dumps(data).encode()
        request = urllib.request.Request(
            f"http://127.0.0.1:{args.base_port+index}{path}", body,
            {"Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=5) as response:
            return json.load(response)

    def wallet(index, command, *flags):
        name = f"node{index+1}"
        return json.loads(run(compose + ["exec", "-T", name,
                              "gochainkit-wallet", command,
                              "--api", "http://127.0.0.1:8080",
                              "--keystore", f"/data/{name}/keys", *flags]))

    def wait(label, check, timeout=90):
        deadline = time.monotonic() + timeout
        last = None
        while time.monotonic() < deadline:
            try:
                value = check()
                if value:
                    results.append({"check": label, "status": "passed"})
                    print(f"PASS {label}", flush=True)
                    return value
            except (OSError, ValueError, RuntimeError) as exc:
                last = str(exc)
            time.sleep(0.3)
        raise RuntimeError(f"Timeout: {label}; last error: {last}")

    def envelope(view):
        return json.loads(base64.b64decode(view["data"]))

    def head(index, inside=False):
        view = wallet(index, "status") if inside else api(index, "/v1/chain/head")
        value = envelope(view)
        return view["id"], value["Header"]["Height"], view

    def same_heads():
        values = [head(i)[0] for i in range(3)]
        return values[0] if len(set(values)) == 1 else False

    def confirmed_on_all(txid):
        values = [api(i, f"/v1/transactions/{txid}") for i in range(3)]
        return (all(v["status"] == "confirmed" for v in values)
                and len({v["blockId"] for v in values}) == 1)

    try:
        if not args.skip_start:
            run(compose + ["up", "-d", "--wait", "--no-build"], timeout=180)
        manifest = json.loads(run(compose + ["exec", "-T", "node1", "cat",
                                            "/network/manifest.json"]))
        wait("three nodes have two peers", lambda: all(
            len(api(i, "/v1/node")["network"]["peers"]) >= 2 for i in range(3)))
        wait("mined rewards mature", lambda: all(head(i)[1] >= 7 for i in range(3))
             and int(wallet(0, "balance")["spendable"]) >= 700001000)
        wait("three nodes converge", same_heads)
        wait("all three miners earn rewards", lambda: all(
            int(api(i, f"/v1/addresses/{node['address']}/utxos")["spendable"]) > 0
            for i, node in enumerate(manifest["nodes"])))
        # Inspect the actual block at a retarget boundary, not just its config.
        def retargeted():
            view = head(0)[2]
            value = envelope(view)
            while value["Header"]["Height"] > 20:
                parent = base64.b64decode(value["Header"]["Parent"]).hex()
                view = api(0, f"/v1/blocks/{parent}")
                value = envelope(view)
            if value["Header"]["Height"] != 20:
                return False
            bits, epoch, _nonce = struct.unpack(">IQQ", base64.b64decode(value["Header"]["Consensus"]))
            if epoch != value["Header"]["Timestamp"]:
                raise RuntimeError("retarget epoch was not reset")
            parent = base64.b64decode(value["Header"]["Parent"]).hex()
            previous = envelope(api(0, f"/v1/blocks/{parent}"))
            actual = previous["Header"]["Timestamp"] - manifest["genesisTime"]
            actual = min(max(actual, 10), 160)  # expected 20*2 seconds, 4x clamp
            target = min((0x7fffff << (8*29)) * actual // 40, 0x7fffff << (8*29))
            size = (target.bit_length() + 7) // 8
            word = target >> (8 * (size-3)) if size > 3 else target << (8 * (3-size))
            if word & 0x800000:
                size += 1
                word >>= 8
            if bits != (size << 24 | word):
                raise AssertionError("retarget differs from historical timestamps")
            return True
        wait("difficulty retarget active", retargeted, timeout=120)

        key_id = f"smoke-{time.time_ns()}"
        recipient = wallet(1, "create", "--key", key_id)["address"]
        sent = wallet(0, "send", "--address", recipient,
                      "--amount", "700000000", "--fee", "1000")
        txid = sent["id"]
        wait("signed transfer confirmed on all nodes", lambda: confirmed_on_all(txid))
        wait("recipient balance agrees on all nodes", lambda: all(
            int(api(i, f"/v1/addresses/{recipient}/utxos")["spendable"]) == 700000000
            for i in range(3)))

        transfer = api(0, f"/v1/transactions/{txid}")
        block = envelope(api(0, f"/v1/blocks/{transfer['blockId']}"))
        transfer_height = block["Header"]["Height"]
        producer = base64.b64decode(block["Header"]["Producer"]).hex()
        reward_coins = wallet(0, "balance", "--address", producer)["coins"]
        subsidy = 5000000000 >> (transfer_height // 150)
        if not any(c["coinbase"] and int(c["createdHeight"]) == transfer_height
                   and int(c["amount"]) == subsidy + 1000 for c in reward_coins):
            raise RuntimeError("coinbase does not include exact subsidy plus fee")
        results.append({"check": "coinbase pays subsidy plus exact fee", "status": "passed"})
        print("PASS coinbase pays subsidy plus exact fee", flush=True)

        def immature_coin():
            values = wallet(0, "balance")["coins"]
            values = [c for c in values if c["coinbase"] and not c["spendable"]]
            return max(values, key=lambda c: int(c["createdHeight"])) if values else False
        miner = manifest["nodes"][0]["address"]

        def sign(raw):
            return json.loads(run(compose + ["exec", "-T", "node1", "gochainkit-wallet",
                                            "sign", "--keystore", "/data/node1/keys"],
                                  input_text=json.dumps(raw)))
        for attempt in range(8):
            coin = wait("immature coinbase available for validation", immature_coin)
            unsigned = {"Network": manifest["network"], "Inputs": [{
                "Previous": {"TransactionID": list(bytes.fromhex(coin["transactionId"])),
                             "Index": int(coin["index"])}, "PublicKey": None, "Signature": None}],
                "Outputs": [{"To": list(bytes.fromhex(miner)), "Amount": int(coin["amount"])-1}]}
            signed = sign(unsigned)
            if head(0)[1] + 1 < int(coin["matureHeight"]):
                break
        else:
            raise RuntimeError("coinbase repeatedly matured before submission")
        api(0, "/v1/transactions", {"data": signed["data"]})

        def matured_confirmation():
            value = api(0, f"/v1/transactions/{signed['id']}")
            if value["status"] != "confirmed":
                return False
            accepted_height = envelope(api(0, f"/v1/blocks/{value['blockId']}"))["Header"]["Height"]
            if accepted_height < int(coin["matureHeight"]):
                raise AssertionError("immature coinbase was spent early")
            return True
        wait("coinbase spend waits until maturity", matured_confirmation)
        wait("mature coinbase spend confirmed on all nodes", lambda: confirmed_on_all(signed["id"]))
        for i in range(3):
            output = api(i, f"/v1/utxos/{signed['id']}/0")
            receipt = api(i, f"/v1/receipts/{signed['id']}")
            if int(output["amount"]) != int(coin["amount"])-1 or base64.b64decode(receipt["data"]) != b"\x01":
                raise RuntimeError("confirmed coinbase spend did not execute")
        unsigned["Outputs"][0]["Amount"] -= 1
        double_spend = sign(unsigned)
        api(0, "/v1/transactions", {"data": double_spend["data"]})
        after_spend = head(0)[1]
        wait("blocks advance after conflicting spend", lambda: head(0)[1] >= after_spend+3)
        for i in range(3):
            try:
                value = api(i, f"/v1/transactions/{double_spend['id']}")
            except urllib.error.HTTPError as exc:
                if exc.code != 404:
                    raise
            else:
                if value["status"] == "confirmed":
                    raise RuntimeError("double spend confirmed")
        results.append({"check": "double spend never enters canonical chain", "status": "passed"})
        print("PASS double spend never enters canonical chain", flush=True)
        wait("original coinbase spend remains canonical", lambda: confirmed_on_all(signed["id"]))

        transaction = api(0, f"/v1/transactions/{txid}")
        raw = json.loads(base64.b64decode(transaction["data"]))
        signature = bytearray(base64.b64decode(raw["Inputs"][0]["Signature"]))
        signature[0] ^= 1
        raw["Inputs"][0]["Signature"] = base64.b64encode(signature).decode()
        encoded = base64.b64encode(json.dumps(raw, separators=(",", ":")).encode()).decode()
        try:
            api(0, "/v1/transactions", {"data": encoded})
        except urllib.error.HTTPError as exc:
            if exc.code != 400:
                raise
        else:
            raise RuntimeError("invalid signature accepted")
        results.append({"check": "invalid signature rejected", "status": "passed"})
        print("PASS invalid signature rejected", flush=True)

        peers_before = [api(i, "/v1/node")["network"]["addresses"] for i in range(3)]
        run(compose + ["restart", "node2"])
        wait("confirmed transaction survives restart", lambda: confirmed_on_all(txid))
        if wallet(1, "address", "--key", key_id)["address"] != recipient:
            raise RuntimeError("wallet identity changed across restart")
        if api(1, "/v1/node")["network"]["addresses"] != peers_before[1]:
            raise RuntimeError("P2P identity changed across restart")

        # Stop node3 while the others advance, then prove it serves the blocks
        # it missed after restarting with the same data volume.
        old_height = head(2)[1]
        run(compose + ["stop", "node3"])
        wait("remaining nodes advance while peer offline", lambda: head(0)[1] >= old_height + 3)
        target = head(0)[0]
        run(compose + ["start", "node3"])
        wait("offline node catches up", lambda: api(2, f"/v1/blocks/{target}")["id"] == target)
        wait("nodes converge after restart", same_heads)

        container = run(compose + ["ps", "-q", "node3"]).strip()
        inspection = json.loads(run(["docker", "inspect", container]))[0]
        networks = inspection["NetworkSettings"]["Networks"]
        network = next(n for n in networks if n.endswith("_chain"))
        before = head(2)[1]
        run(["docker", "network", "disconnect", network, container])
        detached = (network, container)
        wait("both network partitions continue mining", lambda:
             head(0)[1] >= before + 2 and head(2, inside=True)[1] >= before + 2)
        left, right = head(0)[0], head(2, inside=True)[0]
        if left == right:
            raise RuntimeError("partition did not produce competing branches")
        run(["docker", "network", "connect", "--alias", "node3", network, container])
        detached = None
        final_head = wait("partitioned branches converge by cumulative work", same_heads, timeout=120)
        wait("confirmed transfer remains canonical", lambda: confirmed_on_all(txid))
        report = {"project": args.project, "apiPorts": list(range(args.base_port, args.base_port+3)),
                  "transfer": txid, "recipient": recipient, "head": final_head, "checks": results}
        if args.report:
            args.report.parent.mkdir(parents=True, exist_ok=True)
            args.report.write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps(report, indent=2), flush=True)
    except Exception:
        print(run(compose + ["ps", "-a"]), flush=True)
        print(run(compose + ["logs", "--tail", "30"]), flush=True)
        raise
    finally:
        if detached:
            network, container = detached
            run(["docker", "network", "connect", "--alias", "node3", network, container])


if __name__ == "__main__":
    main()
