"""Small selector-bearing block fixtures for Python evidence validators."""


def selector_block(number, height, age, author='0x1111111111111111111111111111111111111111',
                   pass_byte=1, snapshot_byte=3):
    payload = (bytes([1]) + (1).to_bytes(2, 'big') + height.to_bytes(4, 'big') + age.to_bytes(4, 'big')
               + bytes([snapshot_byte]) * 32 + bytes([4]) * 32 + bytes([pass_byte]) * 32 + bytes(4))
    return dict(number=hex(number), hash=f'hash-{number}', parentHash=f'hash-{number - 1}',
                miner=author, extraData='0x' + payload.hex())
