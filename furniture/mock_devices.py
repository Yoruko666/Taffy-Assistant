"""Mock devices (light/aircon/curtain) via MQTT or standalone mode."""

from __future__ import annotations

import argparse
import json
import threading
import time
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional

try:
    import paho.mqtt.client as mqtt
except ImportError as exc:
    print(f"[mock-devices] Missing dependency: {exc}")
    print("Please run: pip install -r furniture/requirements.txt")
    raise SystemExit(1)


TOPIC_REGISTER = "taffy/device/{id}/register"
TOPIC_CMD = "taffy/device/{id}/cmd"
TOPIC_STATUS = "taffy/device/{id}/status"
TOPIC_RESULT = "taffy/device/{id}/result"
TOPIC_HEARTBEAT = "taffy/device/{id}/heartbeat"


@dataclass
class DeviceSimulator:
    device_id: str
    device_type: str
    name: str
    room: str
    power: bool = False
    brightness: Optional[int] = None
    temperature: Optional[int] = None
    mode: Optional[str] = None
    position: Optional[int] = None
    last_updated: float = field(default_factory=time.time)

    def apply_command(self, action: str, params: Dict[str, Any]) -> str:
        action = action.lower()
        if self.device_type == "light":
            return self._apply_light(action, params)
        if self.device_type == "aircon":
            return self._apply_aircon(action, params)
        if self.device_type == "curtain":
            return self._apply_curtain(action, params)
        return "unsupported device"

    def _apply_light(self, action: str, params: Dict[str, Any]) -> str:
        if action in ("turn_on", "on"):
            self.power = True
            if "brightness" in params:
                self.brightness = int(params["brightness"])
        elif action in ("turn_off", "off"):
            self.power = False
        elif action == "set_brightness":
            self.brightness = int(params.get("brightness", 100))
            self.power = True
        else:
            return "unknown action"
        self.last_updated = time.time()
        return "ok"

    def _apply_aircon(self, action: str, params: Dict[str, Any]) -> str:
        if action in ("turn_on", "on"):
            self.power = True
        elif action in ("turn_off", "off"):
            self.power = False
        elif action == "set_temperature":
            self.temperature = int(params.get("temperature", 26))
            self.power = True
        elif action == "set_mode":
            self.mode = str(params.get("mode", "cool"))
            self.power = True
        else:
            return "unknown action"
        self.last_updated = time.time()
        return "ok"

    def _apply_curtain(self, action: str, params: Dict[str, Any]) -> str:
        if action in ("open", "turn_on", "on"):
            self.position = 100
        elif action in ("close", "turn_off", "off"):
            self.position = 0
        elif action == "set_position":
            self.position = int(params.get("position", 0))
        else:
            return "unknown action"
        self.last_updated = time.time()
        return "ok"

    def status_payload(self) -> Dict[str, Any]:
        payload = {
            "device_id": self.device_id,
            "type": self.device_type,
            "name": self.name,
            "room": self.room,
            "power": self.power,
            "updated_at": int(self.last_updated),
        }
        if self.brightness is not None:
            payload["brightness"] = self.brightness
        if self.temperature is not None:
            payload["temperature"] = self.temperature
        if self.mode is not None:
            payload["mode"] = self.mode
        if self.position is not None:
            payload["position"] = self.position
        return payload

    def register_payload(self) -> Dict[str, Any]:
        return {
            "device_id": self.device_id,
            "type": self.device_type,
            "name": self.name,
            "room": self.room,
        }


def parse_devices(raw: str) -> List[DeviceSimulator]:
    devices: List[DeviceSimulator] = []
    for item in raw.split(","):
        item = item.strip()
        if not item:
            continue
        if "_" in item:
            device_type, room = item.split("_", 1)
        else:
            device_type, room = item, ""
        device_id = item
        name = item
        device_type = device_type.lower()
        if device_type not in ("light", "aircon", "curtain"):
            raise ValueError(f"Unsupported device type: {device_type}")
        devices.append(DeviceSimulator(
            device_id=device_id,
            device_type=device_type,
            name=name,
            room=room,
            brightness=80 if device_type == "light" else None,
            temperature=26 if device_type == "aircon" else None,
            mode="cool" if device_type == "aircon" else None,
            position=0 if device_type == "curtain" else None,
        ))
    if not devices:
        raise ValueError("No devices specified")
    return devices


def run_standalone(devices: List[DeviceSimulator]) -> None:
    print("[standalone] devices:")
    for dev in devices:
        print(f"  {dev.device_id} ({dev.device_type}) room={dev.room}")
    print("[standalone] cycling states every 10s (Ctrl+C to stop)")
    tick = 0
    try:
        while True:
            time.sleep(10)
            tick += 1
            for dev in devices:
                if dev.device_type == "light":
                    dev.power = not dev.power
                elif dev.device_type == "aircon":
                    dev.power = not dev.power
                    dev.temperature = 24 if dev.power else 26
                elif dev.device_type == "curtain":
                    dev.position = 100 if dev.position == 0 else 0
                dev.last_updated = time.time()
                print(f"[standalone] {dev.device_id} state={dev.status_payload()}")
    except KeyboardInterrupt:
        print("\n[standalone] stopped")


def main() -> int:
    parser = argparse.ArgumentParser(description="Mock devices via MQTT or standalone mode")
    parser.add_argument("--broker", default="localhost", help="MQTT broker host")
    parser.add_argument("--port", type=int, default=1883, help="MQTT broker port")
    parser.add_argument("--devices", required=True, help="Comma-separated list, e.g. light_living,aircon_bedroom")
    parser.add_argument("--standalone", action="store_true", help="Run without MQTT broker")
    args = parser.parse_args()

    devices = parse_devices(args.devices)
    if args.standalone:
        run_standalone(devices)
        return 0

    device_map = {dev.device_id: dev for dev in devices}
    client = mqtt.Client()

    def publish(topic: str, payload: Dict[str, Any], qos: int = 1) -> None:
        client.publish(topic, json.dumps(payload, ensure_ascii=False), qos=qos)

    def on_connect(_client, _userdata, _flags, rc):
        if rc != 0:
            print(f"[mqtt] connect failed: {rc}")
            return
        print(f"[mqtt] connected to {args.broker}:{args.port}")
        for dev in devices:
            cmd_topic = TOPIC_CMD.format(id=dev.device_id)
            client.subscribe(cmd_topic, qos=1)
            publish(TOPIC_REGISTER.format(id=dev.device_id), dev.register_payload(), qos=1)
            publish(TOPIC_STATUS.format(id=dev.device_id), dev.status_payload(), qos=1)

    def on_message(_client, _userdata, msg):
        dev_id = msg.topic.split("/")[2] if msg.topic.count("/") >= 3 else ""
        dev = device_map.get(dev_id)
        if not dev:
            print(f"[mqtt] unknown device topic: {msg.topic}")
            return
        try:
            payload = json.loads(msg.payload.decode("utf-8"))
        except json.JSONDecodeError:
            print(f"[mqtt] invalid json: {msg.payload!r}")
            return
        action = payload.get("action", "")
        params = payload.get("params", {})
        result = dev.apply_command(action, params)
        print(f"[mqtt] {dev.device_id} cmd={action} params={params} result={result}")
        publish(TOPIC_STATUS.format(id=dev.device_id), dev.status_payload(), qos=1)
        publish(TOPIC_RESULT.format(id=dev.device_id), {
            "device_id": dev.device_id,
            "action": action,
            "result": result,
        }, qos=1)

    client.on_connect = on_connect
    client.on_message = on_message
    client.connect(args.broker, args.port, keepalive=30)
    client.loop_start()

    def heartbeat_loop() -> None:
        while True:
            for dev in devices:
                publish(TOPIC_HEARTBEAT.format(id=dev.device_id), {
                    "device_id": dev.device_id,
                    "ts": int(time.time()),
                }, qos=0)
            time.sleep(30)

    hb_thread = threading.Thread(target=heartbeat_loop, daemon=True)
    hb_thread.start()

    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        print("\n[mqtt] stopped")
        client.loop_stop()
        client.disconnect()
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
